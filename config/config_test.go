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

package config

import (
	"encoding/json"
	"io/ioutil"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	config_util "github.com/prometheus/common/config"
	"github.com/prometheus/common/model"

	sd_config "github.com/torch/discovery/config"
	"github.com/torch/util/testutil"
	"github.com/torch/discovery/kubernetes"
	"github.com/torch/discovery/targetgroup"

	"gopkg.in/yaml.v2"
)

func mustParseURL(u string) *config_util.URL {
	parsed, err := url.Parse(u)
	if err != nil {
		panic(err)
	}
	return &config_util.URL{URL: parsed}
}

var expectedConf = &Config{
	GlobalConfig: GlobalConfig{
		ScrapeInterval:     model.Duration(15 * time.Second),
		ScrapeTimeout:      DefaultGlobalConfig.ScrapeTimeout,
		EvaluationInterval: model.Duration(30 * time.Second),

		ExternalLabels: model.LabelSet{
			"monitor": "codelab",
			"foo":     "bar",
		},
	},

	RuleFiles: []string{
		filepath.FromSlash("testdata/first.rules"),
		filepath.FromSlash("testdata/my/*.rules"),
	},

	RemoteWriteConfigs: []*RemoteWriteConfig{
		{
			URL:           mustParseURL("http://remote1/push"),
			RemoteTimeout: model.Duration(30 * time.Second),
			WriteRelabelConfigs: []*RelabelConfig{
				{
					SourceLabels: model.LabelNames{"__name__"},
					Separator:    ";",
					Regex:        MustNewRegexp("expensive.*"),
					Replacement:  "$1",
					Action:       RelabelDrop,
				},
			},
			QueueConfig: DefaultQueueConfig,
		},
		{
			URL:           mustParseURL("http://remote2/push"),
			RemoteTimeout: model.Duration(30 * time.Second),
			QueueConfig:   DefaultQueueConfig,
		},
	},

	RemoteReadConfigs: []*RemoteReadConfig{
		{
			URL:           mustParseURL("http://remote1/read"),
			RemoteTimeout: model.Duration(1 * time.Minute),
			ReadRecent:    true,
		},
		{
			URL:              mustParseURL("http://remote3/read"),
			RemoteTimeout:    model.Duration(1 * time.Minute),
			ReadRecent:       false,
			RequiredMatchers: model.LabelSet{"job": "special"},
		},
	},

	ScrapeConfigs: []*ScrapeConfig{
		{
			JobName: "prometheus",

			HonorLabels:    true,
			ScrapeInterval: model.Duration(15 * time.Second),
			ScrapeTimeout:  DefaultGlobalConfig.ScrapeTimeout,

			MetricsPath: DefaultScrapeConfig.MetricsPath,
			Scheme:      DefaultScrapeConfig.Scheme,

			HTTPClientConfig: config_util.HTTPClientConfig{
				BearerTokenFile: filepath.FromSlash("testdata/valid_token_file"),
			},

			ServiceDiscoveryConfig: sd_config.ServiceDiscoveryConfig{

			},

			RelabelConfigs: []*RelabelConfig{
				{
					SourceLabels: model.LabelNames{"job", "__meta_dns_name"},
					TargetLabel:  "job",
					Separator:    ";",
					Regex:        MustNewRegexp("(.*)some-[regex]"),
					Replacement:  "foo-${1}",
					Action:       RelabelReplace,
				}, {
					SourceLabels: model.LabelNames{"abc"},
					TargetLabel:  "cde",
					Separator:    ";",
					Regex:        DefaultRelabelConfig.Regex,
					Replacement:  DefaultRelabelConfig.Replacement,
					Action:       RelabelReplace,
				}, {
					TargetLabel: "abc",
					Separator:   ";",
					Regex:       DefaultRelabelConfig.Regex,
					Replacement: "static",
					Action:      RelabelReplace,
				}, {
					TargetLabel: "abc",
					Separator:   ";",
					Regex:       MustNewRegexp(""),
					Replacement: "static",
					Action:      RelabelReplace,
				},
			},
		},
		{

			JobName: "service-x",

			ScrapeInterval: model.Duration(50 * time.Second),
			ScrapeTimeout:  model.Duration(5 * time.Second),
			SampleLimit:    1000,

			HTTPClientConfig: config_util.HTTPClientConfig{
				BasicAuth: &config_util.BasicAuth{
					Username: "admin_name",
					Password: "multiline\nmysecret\ntest",
				},
			},
			MetricsPath: "/my_path",
			Scheme:      "https",

			ServiceDiscoveryConfig: sd_config.ServiceDiscoveryConfig{

			},

			RelabelConfigs: []*RelabelConfig{
				{
					SourceLabels: model.LabelNames{"job"},
					Regex:        MustNewRegexp("(.*)some-[regex]"),
					Separator:    ";",
					Replacement:  DefaultRelabelConfig.Replacement,
					Action:       RelabelDrop,
				},
				{
					SourceLabels: model.LabelNames{"__address__"},
					TargetLabel:  "__tmp_hash",
					Regex:        DefaultRelabelConfig.Regex,
					Replacement:  DefaultRelabelConfig.Replacement,
					Modulus:      8,
					Separator:    ";",
					Action:       RelabelHashMod,
				},
				{
					SourceLabels: model.LabelNames{"__tmp_hash"},
					Regex:        MustNewRegexp("1"),
					Separator:    ";",
					Replacement:  DefaultRelabelConfig.Replacement,
					Action:       RelabelKeep,
				},
				{
					Regex:       MustNewRegexp("1"),
					Separator:   ";",
					Replacement: DefaultRelabelConfig.Replacement,
					Action:      RelabelLabelMap,
				},
				{
					Regex:       MustNewRegexp("d"),
					Separator:   ";",
					Replacement: DefaultRelabelConfig.Replacement,
					Action:      RelabelLabelDrop,
				},
				{
					Regex:       MustNewRegexp("k"),
					Separator:   ";",
					Replacement: DefaultRelabelConfig.Replacement,
					Action:      RelabelLabelKeep,
				},
			},
			MetricRelabelConfigs: []*RelabelConfig{
				{
					SourceLabels: model.LabelNames{"__name__"},
					Regex:        MustNewRegexp("expensive_metric.*"),
					Separator:    ";",
					Replacement:  DefaultRelabelConfig.Replacement,
					Action:       RelabelDrop,
				},
			},
		},
		{
			JobName: "service-kubernetes",

			ScrapeInterval: model.Duration(15 * time.Second),
			ScrapeTimeout:  DefaultGlobalConfig.ScrapeTimeout,

			MetricsPath: DefaultScrapeConfig.MetricsPath,
			Scheme:      DefaultScrapeConfig.Scheme,

			ServiceDiscoveryConfig: sd_config.ServiceDiscoveryConfig{
				KubernetesSDConfigs: []*kubernetes.SDConfig{
					{
						APIServer: kubernetesSDHostURL(),
						Role:      kubernetes.RoleEndpoint,
						BasicAuth: &config_util.BasicAuth{
							Username: "myusername",
							Password: "mysecret",
						},
						NamespaceDiscovery: kubernetes.NamespaceDiscovery{},
					},
				},
			},
		},
		{
			JobName: "service-kubernetes-namespaces",

			ScrapeInterval: model.Duration(15 * time.Second),
			ScrapeTimeout:  DefaultGlobalConfig.ScrapeTimeout,

			MetricsPath: DefaultScrapeConfig.MetricsPath,
			Scheme:      DefaultScrapeConfig.Scheme,

			ServiceDiscoveryConfig: sd_config.ServiceDiscoveryConfig{
				KubernetesSDConfigs: []*kubernetes.SDConfig{
					{
						APIServer: kubernetesSDHostURL(),
						Role:      kubernetes.RoleEndpoint,
						NamespaceDiscovery: kubernetes.NamespaceDiscovery{
							Names: []string{
								"default",
							},
						},
					},
				},
			},
		},
	},
	AlertingConfig: AlertingConfig{
		AlertmanagerConfigs: []*AlertmanagerConfig{
			{
				Scheme:  "https",
				Timeout: 10 * time.Second,
				ServiceDiscoveryConfig: sd_config.ServiceDiscoveryConfig{

				},
			},
		},
	},
	original: "",
}

func TestLoadConfig(t *testing.T) {
	// Parse a valid file that sets a global scrape timeout. This tests whether parsing
	// an overwritten default field in the global config permanently changes the default.
	_, err := LoadFile("testdata/global_timeout.good.yml")
	testutil.Ok(t, err)

	c, err := LoadFile("testdata/conf.good.yml")
	testutil.Ok(t, err)

	expectedConf.original = c.original
	testutil.Equals(t, expectedConf, c)
}

// YAML marshalling must not reveal authentication credentials.
func TestElideSecrets(t *testing.T) {
	c, err := LoadFile("testdata/conf.good.yml")
	testutil.Ok(t, err)

	secretRe := regexp.MustCompile(`\\u003csecret\\u003e|<secret>`)

	config, err := yaml.Marshal(c)
	testutil.Ok(t, err)
	yamlConfig := string(config)

	matches := secretRe.FindAllStringIndex(yamlConfig, -1)
	testutil.Assert(t, len(matches) == 6, "wrong number of secret matches found")
	testutil.Assert(t, !strings.Contains(yamlConfig, "mysecret"),
		"yaml marshal reveals authentication credentials.")
}

func TestLoadConfigRuleFilesAbsolutePath(t *testing.T) {
	// Parse a valid file that sets a rule files with an absolute path
	c, err := LoadFile(ruleFilesConfigFile)
	testutil.Ok(t, err)

	ruleFilesExpectedConf.original = c.original
	testutil.Equals(t, ruleFilesExpectedConf, c)
}

var expectedErrors = []struct {
	filename string
	errMsg   string
}{
	{
		filename: "jobname.bad.yml",
		errMsg:   `job_name is empty`,
	}, {
		filename: "jobname_dup.bad.yml",
		errMsg:   `found multiple scrape configs with job name "prometheus"`,
	}, {
		filename: "scrape_interval.bad.yml",
		errMsg:   `scrape timeout greater than scrape interval`,
	}, {
		filename: "labelname.bad.yml",
		errMsg:   `"not$allowed" is not a valid label name`,
	}, {
		filename: "labelname2.bad.yml",
		errMsg:   `"not:allowed" is not a valid label name`,
	}, {
		filename: "regex.bad.yml",
		errMsg:   "error parsing regexp",
	}, {
		filename: "modulus_missing.bad.yml",
		errMsg:   "relabel configuration for hashmod requires non-zero modulus",
	}, {
		filename: "labelkeep.bad.yml",
		errMsg:   "labelkeep action requires only 'regex', and no other fields",
	}, {
		filename: "labelkeep2.bad.yml",
		errMsg:   "labelkeep action requires only 'regex', and no other fields",
	}, {
		filename: "labelkeep3.bad.yml",
		errMsg:   "labelkeep action requires only 'regex', and no other fields",
	}, {
		filename: "labelkeep4.bad.yml",
		errMsg:   "labelkeep action requires only 'regex', and no other fields",
	}, {
		filename: "labelkeep5.bad.yml",
		errMsg:   "labelkeep action requires only 'regex', and no other fields",
	}, {
		filename: "labeldrop.bad.yml",
		errMsg:   "labeldrop action requires only 'regex', and no other fields",
	}, {
		filename: "labeldrop2.bad.yml",
		errMsg:   "labeldrop action requires only 'regex', and no other fields",
	}, {
		filename: "labeldrop3.bad.yml",
		errMsg:   "labeldrop action requires only 'regex', and no other fields",
	}, {
		filename: "labeldrop4.bad.yml",
		errMsg:   "labeldrop action requires only 'regex', and no other fields",
	}, {
		filename: "labeldrop5.bad.yml",
		errMsg:   "labeldrop action requires only 'regex', and no other fields",
	}, {
		filename: "rules.bad.yml",
		errMsg:   "invalid rule file path",
	}, {
		filename: "unknown_attr.bad.yml",
		errMsg:   "unknown fields in scrape_config: consult_sd_configs",
	}, {
		filename: "bearertoken.bad.yml",
		errMsg:   "at most one of bearer_token & bearer_token_file must be configured",
	}, {
		filename: "bearertoken_basicauth.bad.yml",
		errMsg:   "at most one of basic_auth, bearer_token & bearer_token_file must be configured",
	}, {
		filename: "kubernetes_bearertoken.bad.yml",
		errMsg:   "at most one of bearer_token & bearer_token_file must be configured",
	}, {
		filename: "kubernetes_role.bad.yml",
		errMsg:   "role",
	}, {
		filename: "kubernetes_namespace_discovery.bad.yml",
		errMsg:   "unknown fields in namespaces",
	}, {
		filename: "kubernetes_bearertoken_basicauth.bad.yml",
		errMsg:   "at most one of basic_auth, bearer_token & bearer_token_file must be configured",
	}, {
		filename: "marathon_no_servers.bad.yml",
		errMsg:   "Marathon SD config must contain at least one Marathon server",
	}, {
		filename: "url_in_targetgroup.bad.yml",
		errMsg:   "\"http://bad\" is not a valid hostname",
	}, {
		filename: "target_label_missing.bad.yml",
		errMsg:   "relabel configuration for replace action requires 'target_label' value",
	}, {
		filename: "target_label_hashmod_missing.bad.yml",
		errMsg:   "relabel configuration for hashmod action requires 'target_label' value",
	}, {
		filename: "unknown_global_attr.bad.yml",
		errMsg:   "unknown fields in global config: nonexistent_field",
	}, {
		filename: "remote_read_url_missing.bad.yml",
		errMsg:   `url for remote_read is empty`,
	}, {
		filename: "remote_write_url_missing.bad.yml",
		errMsg:   `url for remote_write is empty`,
	},
}

func TestBadConfigs(t *testing.T) {
	for _, ee := range expectedErrors {
		_, err := LoadFile("testdata/" + ee.filename)
		testutil.NotOk(t, err, "%s", ee.filename)
		testutil.Assert(t, strings.Contains(err.Error(), ee.errMsg),
			"Expected error for %s to contain %q but got: %s", ee.filename, ee.errMsg, err)
	}
}

func TestBadStaticConfigs(t *testing.T) {
	content, err := ioutil.ReadFile("testdata/static_config.bad.json")
	testutil.Ok(t, err)
	var tg targetgroup.Group
	err = json.Unmarshal(content, &tg)
	testutil.NotOk(t, err, "")
}

func TestEmptyConfig(t *testing.T) {
	c, err := Load("")
	testutil.Ok(t, err)
	exp := DefaultConfig
	testutil.Equals(t, exp, *c)
}

func TestEmptyGlobalBlock(t *testing.T) {
	c, err := Load("global:\n")
	testutil.Ok(t, err)
	exp := DefaultConfig
	exp.original = "global:\n"
	testutil.Equals(t, exp, *c)
}

func TestTargetLabelValidity(t *testing.T) {
	tests := []struct {
		str   string
		valid bool
	}{
		{"-label", false},
		{"label", true},
		{"label${1}", true},
		{"${1}label", true},
		{"${1}", true},
		{"${1}label", true},
		{"${", false},
		{"$", false},
		{"${}", false},
		{"foo${", false},
		{"$1", true},
		{"asd$2asd", true},
		{"-foo${1}bar-", false},
		{"_${1}_", true},
		{"foo${bar}foo", true},
	}
	for _, test := range tests {
		testutil.Assert(t, relabelTarget.Match([]byte(test.str)) == test.valid,
			"Expected %q to be %v", test.str, test.valid)
	}
}

func kubernetesSDHostURL() config_util.URL {
	tURL, _ := url.Parse("https://localhost:1234")
	return config_util.URL{URL: tURL}
}
