// Copyright 2015 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// The main package for the Prometheus server executable.
package main

import (
	"context"
	"fmt"
//	_ "net/http/pprof" // Comment this line to disable pprof endpoint.
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"gopkg.in/alecthomas/kingpin.v2"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/oklog/oklog/pkg/group"
	"github.com/pkg/errors"

	"github.com/prometheus/common/model"
	"github.com/prometheus/common/version"
	"github.com/prometheus/common/promlog"
	promlogflag "github.com/prometheus/common/promlog/flag"

	k8s_runtime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/torch/config"
	"github.com/torch/discovery"
	sd_config "github.com/torch/discovery/config"
	"github.com/torch/collector"
)

func main() {
	if os.Getenv("DEBUG") != "" {
		runtime.SetBlockProfileRate(20)
		runtime.SetMutexProfileFraction(20)
	}

	cfg := struct {
		configFile    string
		queryTimeout  model.Duration
		logLevel      promlog.AllowedLevel
		outputDir	  string
		reloadURL     string
	}{}

	a := kingpin.New(filepath.Base(os.Args[0]), "Torch for Prometheus monitoring server")

	a.Version(version.Print("torch"))

	a.HelpFlag.Short('h')

	a.Flag("config.file", "Prometheus configuration file path.").
		Default("prometheus.yml").StringVar(&cfg.configFile)

	a.Flag("output", "Directory to save received rules").
		Default("/etc/custom-rules").StringVar(&cfg.outputDir)

	a.Flag("reload-url", "Prometheus reload url.").
		Default("http://localhost:9090/-/reload").StringVar(&cfg.reloadURL)

	a.Flag("query.timeout", "Maximum time a query may take before being aborted.").
		Default("2m").SetValue(&cfg.queryTimeout)

	promlogflag.AddFlags(a, &cfg.logLevel)

	_, err := a.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, errors.Wrapf(err, "Error parsing commandline arguments"))
		a.Usage(os.Args[1:])
		os.Exit(2)
	}

	logger := promlog.New(cfg.logLevel)

	// XXX(fabxc): Kubernetes does background logging which we can only customize by modifying
	// a global variable.
	// Ultimately, here is the best place to set it.
	k8s_runtime.ErrorHandlers = []func(error){
		func(err error) {
			level.Error(log.With(logger, "component", "k8s_client_runtime")).Log("err", err)
		},
	}

	level.Info(logger).Log("msg", "Starting", "version", version.Info())
	level.Info(logger).Log("build_context", version.BuildContext())

	var (
		ctxNotify, cancelNotify = context.WithCancel(context.Background())
		discoveryManager        = discovery.NewManager(ctxNotify, log.With(logger, "component", "discovery manager notify"))
		collectorManager 		= collector.NewManager(log.With(logger, "component", "collector manager"), cfg.outputDir, cfg.reloadURL)
	)

	reloaders := []func(cfg *config.Config) error{
		collectorManager.ApplyConfig,
		func(cfg *config.Config) error {
			c := make(map[string]sd_config.ServiceDiscoveryConfig)
			 for _, v := range cfg.ScrapeConfigs {
			 	c[v.JobName] = v.ServiceDiscoveryConfig
			 }
			return discoveryManager.ApplyConfig(c)
		},
	}

	// sync.Once is used to make sure we can close the channel at different execution stages(SIGTERM or when the config is loaded).
	type closeOnce struct {
		C     chan struct{}
		once  sync.Once
		Close func()
	}
	// Wait until the server is ready to handle reloading.
	reloadReady := &closeOnce{
		C: make(chan struct{}),
	}
	reloadReady.Close = func() {
		reloadReady.once.Do(func() {
			close(reloadReady.C)
		})
	}

	var g group.Group
	{
		term := make(chan os.Signal)
		signal.Notify(term, os.Interrupt, syscall.SIGTERM)
		cancel := make(chan struct{})
		g.Add(
			func() error {
				// Don't forget to release the reloadReady channel so that waiting blocks can exit normally.
				select {
				case <-term:
					level.Warn(logger).Log("msg", "Received SIGTERM, exiting gracefully...")
					reloadReady.Close()

				case <-cancel:
					reloadReady.Close()
					break
				}
				return nil
			},
			func(err error) {
				close(cancel)
			},
		)
	}
	{
		g.Add(
			func() error {
				err := discoveryManager.Run()
				level.Info(logger).Log("msg", "Notify discovery manager stopped")
				return err
			},
			func(err error) {
				level.Info(logger).Log("msg", "Stopping notify discovery manager...")
				cancelNotify()
			},
		)
	}
	{
		g.Add(
			func() error {
				// When the scrape manager receives a new targets list
				// it needs to read a valid config for each job.
				// It depends on the config being in sync with the discovery manager so
				// we wait until the config is fully loaded.
				select {
				case <-reloadReady.C:
					break
				}

				err := collectorManager.Run(discoveryManager.SyncCh())
				level.Info(logger).Log("msg", "Collector manager stopped")
				return err
			},
			func(err error) {
				// Scrape manager needs to be stopped before closing the local TSDB
				// so that it doesn't try to write samples to a closed storage.
				level.Info(logger).Log("msg", "Stopping collector manager...")
				collectorManager.Stop()
			},
		)
	}
	{
		cancel := make(chan struct{})
		g.Add(
			func() error {

				if err := reloadConfig(cfg.configFile, logger, reloaders...); err != nil {
					return fmt.Errorf("error loading config %s", err)
				}

				reloadReady.Close()

				level.Info(logger).Log("msg", "READY")
				<-cancel
				return nil
			},
			func(err error) {
				close(cancel)
			},
		)
	}

	if err := g.Run(); err != nil {
		level.Error(logger).Log("err", err)
	}

	level.Info(logger).Log("msg", "Done!")
}

func reloadConfig(filename string, logger log.Logger, rls ...func(*config.Config) error) (err error) {
	level.Info(logger).Log("msg", "Loading configuration file", "filename", filename)

	conf, err := config.LoadFile(filename)
	if err != nil {
		return fmt.Errorf("couldn't load configuration (--config.file=%s): %v", filename, err)
	}

	failed := false
	for _, rl := range rls {
		if err := rl(conf); err != nil {
			level.Error(logger).Log("msg", "Failed to apply configuration", "err", err)
			failed = true
		}
	}
	if failed {
		return fmt.Errorf("one or more errors occurred while applying the new configuration (--config.file=%s)", filename)
	}
	return nil
}

func startsOrEndsWithQuote(s string) bool {
	return strings.HasPrefix(s, "\"") || strings.HasPrefix(s, "'") ||
		strings.HasSuffix(s, "\"") || strings.HasSuffix(s, "'")
}
