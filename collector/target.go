package collector

import (
	"net/url"
	"strings"
	"hash/fnv"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/pkg/labels"
)

// TargetHealth describes the health state of a target.
type TargetHealth string

// The possible health states of a target based on the last performed scrape.
const (
	HealthUnknown TargetHealth = "unknown"
	HealthGood    TargetHealth = "up"
	HealthBad     TargetHealth = "down"
)

// Target refers to a singular HTTP or HTTPS endpoint.
type Target struct {
	// Labels before any processing.
	discoveredLabels labels.Labels
	// Any labels that are added to this target and its metrics.
	labels labels.Labels
	// Additional URL parmeters that are part of the target URL.
	params url.Values

	mtx        sync.RWMutex
	lastError  error
	lastScrape time.Time
	health     TargetHealth
}

// NewTarget creates a reasonably configured target for querying.
func NewTarget(labels, discoveredLabels labels.Labels, params url.Values) *Target {
	return &Target{
		labels:           labels,
		discoveredLabels: discoveredLabels,
		params:           params,
		health:           HealthUnknown,
	}
}

// URL returns a copy of the target's URL.
func (t *Target) URL() *url.URL {
	params := url.Values{}

	for k, v := range t.params {
		params[k] = make([]string, len(v))
		copy(params[k], v)
	}
	for _, l := range t.labels {
		if !strings.HasPrefix(l.Name, model.ParamLabelPrefix) {
			continue
		}
		ks := l.Name[len(model.ParamLabelPrefix):]

		if len(params[ks]) > 0 {
			params[ks][0] = string(l.Value)
		} else {
			params[ks] = []string{l.Value}
		}
	}

	return &url.URL{
		Scheme:   string(t.labels.Get(model.SchemeLabel)),
		Host:     string(t.labels.Get(model.AddressLabel)),
		Path:     string(t.labels.Get(model.MetricsPathLabel)),
		RawQuery: params.Encode(),
	}
}

func (t *Target) String() string {
	return t.URL().String()
}

// Labels returns a copy of the set of all public labels of the target.
func (t *Target) Labels() labels.Labels {
	lset := make(labels.Labels, 0, len(t.labels))
	for _, l := range t.labels {
		if !strings.HasPrefix(l.Name, model.ReservedLabelPrefix) {
			lset = append(lset, l)
		}
	}
	return lset
}

// DiscoveredLabels returns a copy of the target's labels before any processing.
func (t *Target) DiscoveredLabels() labels.Labels {
	lset := make(labels.Labels, len(t.discoveredLabels))
	copy(lset, t.discoveredLabels)
	return lset
}

// hash returns an identifying hash for the target.
func (t *Target) hash() uint64 {
	h := fnv.New64a()
	h.Write([]byte(fmt.Sprintf("%016d", t.labels.Hash())))
	h.Write([]byte(t.URL().String()))

	return h.Sum64()
}



