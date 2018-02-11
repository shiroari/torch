package collector

import (
	"testing"
	"github.com/torch/config"
	"github.com/prometheus/common/promlog"
)

func TestManager_ExtractRuleDirectory(t *testing.T) {

	//log.NewNopLogger()

	ll := promlog.AllowedLevel{}
	ll.Set("debug")

	logger := promlog.New(ll)

	manager := NewManager(logger, "", "")

	cfg := &config.Config {
		ScrapeConfigs: []*config.ScrapeConfig{},
		RuleFiles: []string{
			"collector/*.yaml",
			"collector/testdata/*.yaml",
			"not_exists/*.yaml",
			"file.yaml",
		},
	}

	manager.ApplyConfig(cfg)

	if manager.outputDir != "collector/testdata" {
		t.Fatalf("'%s' != 'collector/testdata'", manager.outputDir)
	}

}
