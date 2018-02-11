package collector

import (
	"sync"
	"fmt"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"

	"github.com/torch/config"
	"github.com/torch/discovery/targetgroup"
	"path/filepath"
	"os"
)

// NewManager is the Manager constructor
func NewManager(logger log.Logger, outputDir, reloadUrl string) *Manager {
	return &Manager{
		outputDir:	   outputDir,
		reloadUrl:	   reloadUrl,
		logger:        logger,
		scrapeConfigs: make(map[string]*config.ScrapeConfig),
		scrapePools:   make(map[string]*scrapePool),
		graceShut:     make(chan struct{}),
	}
}

// Manager maintains a set of scrape pools and manages start/stop cycles
// when receiving new target groups form the discovery manager.
type Manager struct {
	logger        log.Logger
	outputDir	  string
	reloadUrl	  string
	scrapeConfigs map[string]*config.ScrapeConfig
	scrapePools   map[string]*scrapePool
	mtx           sync.RWMutex
	graceShut     chan struct{}
}

// Run starts background processing to handle target updates and reload the scraping loops.
func (m *Manager) Run(tsets <-chan map[string][]*targetgroup.Group) error {
	for {
		select {
		case ts := <-tsets:
			m.reload(ts)
		case <-m.graceShut:
			return nil
		}
	}
}

// Stop cancels all running scrape pools and blocks until all have exited.
func (m *Manager) Stop() {
	close(m.graceShut)
}

// ApplyConfig resets the manager's target providers and job configurations as defined by the new cfg.
func (m *Manager) ApplyConfig(cfg *config.Config) error {
	m.mtx.Lock()
	defer m.mtx.Unlock()
	c := make(map[string]*config.ScrapeConfig)
	for _, scfg := range cfg.ScrapeConfigs {
		c[scfg.JobName] = scfg
	}
	m.scrapeConfigs = c
	m.updateOutputDir(cfg)
	return nil
}

func (m *Manager) updateOutputDir(cfg *config.Config) {
	if m.outputDir != "" {
		return
	}
	for i := len(cfg.RuleFiles) - 1; i >= 0; i-- {
		dir := filepath.Dir(cfg.RuleFiles[i])
		if dir != "." {
			stat, err := os.Lstat(dir)
			if err == nil && stat.IsDir() {
				m.outputDir = dir
				return
			}
		}
	}
	level.Warn(m.logger).Log("msg", "cannot retrieve rules directory from configuration")
}

func (m *Manager) reload(t map[string][]*targetgroup.Group) {
	for tsetName, tgroup := range t {
		scrapeConfig, ok := m.scrapeConfigs[tsetName]
		if !ok {
			level.Error(m.logger).Log("msg", "error reloading target set", "err", fmt.Sprintf("invalid config id:%v", tsetName))
			continue
		}

		// Scrape pool doesn't exist so start a new one.
		existing, ok := m.scrapePools[tsetName]
		if !ok {
			sp := newScrapePool(m.outputDir, m.reloadUrl, scrapeConfig, log.With(m.logger, "scrape_pool", tsetName))
			m.scrapePools[tsetName] = sp
			sp.Sync(tgroup)
		} else {
			existing.Sync(tgroup)
		}
	}
}
