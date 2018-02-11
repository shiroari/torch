package collector

import (
	"net/http/httptest"
	"testing"
	"context"
	"time"
	"net/url"
	"net/http"
	"fmt"

	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/common/model"
	"github.com/prometheus/common/promlog"
	"github.com/go-kit/kit/log/level"
)

func TestExtractor_Update1(t *testing.T) {

	//log.NewNopLogger()

	ll := promlog.AllowedLevel{}
	ll.Set("debug")

	logger := promlog.New(ll)


	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		level.Debug(logger).Log("msg", "request", "url", r.RequestURI)

		if r.RequestURI == "/alerts" {
			fmt.Fprintln(w, "alert text")
			return
		}

		if r.RequestURI == "/-/reload" {
			return
		}

		http.Error(w, "ERROR", http.StatusInternalServerError)

	}))
	defer ts.Close()

	address, _ := url.Parse(ts.URL)

	lbs := labels.Labels{
		labels.Label{Name: "kubernetes_namespace", Value: "my-namespace"},
		labels.Label{Name: "kubernetes_name", Value: "my-service"},
		labels.Label{Name: model.SchemeLabel, Value: address.Scheme},
		labels.Label{Name: model.AddressLabel, Value: fmt.Sprintf("%v:%v", address.Hostname(), address.Port())},
		labels.Label{Name: model.MetricsPathLabel, Value: "/metrics"},
	}

	values := make(map[string][]string)

	extractor := targetExtractor{
		Target: NewTarget(lbs, labels.Labels{}, values),
		client: ts.Client(),
		timeout: time.Minute * 3,
		logger: logger,
		reloadUrl: ts.URL + "/-/reload",
		outputDir: "./output",
	}

	if err := extractor.extractAndSave(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := extractor.extractAndSave(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := extractor.delete(); err != nil {
		t.Fatal(err)
	}

}