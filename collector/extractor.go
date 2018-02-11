package collector

import (
	"time"
	"fmt"
	"os"
	"io/ioutil"
	"context"
	"net/http"

	"github.com/go-kit/kit/log/level"
	"github.com/go-kit/kit/log"

	"path/filepath"
	"golang.org/x/net/context/ctxhttp"
	"strings"
	"errors"
)

type extractor interface {
	extractAndSave(ctx context.Context) error
	delete() error
}

type targetExtractor struct {
	*Target

	client  *http.Client
	timeout time.Duration

	logger log.Logger

	reloadUrl string
	outputDir string
}

func (s *targetExtractor) mkFilename() string {
	filename := fmt.Sprintf("%s_%s.yaml", s.labels.Get("kubernetes_namespace"),
		s.labels.Get("kubernetes_name"))
	return filepath.Join(s.outputDir, filename)
}

func (s *targetExtractor) extractAndSave(ctx context.Context) error {

	url := strings.Replace(s.URL().String(), "metrics", "alerts", 1)

	level.Debug(s.logger).Log("msg", "getting alerts", "url", url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := ctxhttp.Do(ctx, s.client, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		level.Warn(s.logger).Log("msg", "endpoint not found", "url", url)
		return nil
	}

	contents, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	path := s.mkFilename()

	if err := ioutil.WriteFile(path, []byte(contents), os.ModePerm); err != nil {
		return err
	}

	level.Info(s.logger).Log("msg", "rule saved", "file", path)

	s.updatePrometheus(path)

	return nil
}

func (s *targetExtractor) delete() error {
	path := s.mkFilename()
	err := os.Remove(path)
	if err == nil {
		level.Info(s.logger).Log("msg", "rule removed", "file", path)
	} else {
		level.Error(s.logger).Log("file", path, "err", err)
	}
	s.updatePrometheus(path)
	return err
}

func (s *targetExtractor) updatePrometheus(path string) error {
	level.Debug(s.logger).Log("msg", "reloading configuration", "file", path, "url", s.reloadUrl)
	if err := s.updatePrometheusWithError(); err != nil {
		level.Error(s.logger).Log("msg", "cannot reload configuration", "file", path, "err", err)
	}
	level.Debug(s.logger).Log("msg", "configuration reloaded", "file", path)
	return nil
}

func (s *targetExtractor) updatePrometheusWithError() error {

	req, err := http.NewRequest("POST", s.reloadUrl, nil)
	if err != nil {
		return err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return errors.New(resp.Status)
	}

	return nil
}