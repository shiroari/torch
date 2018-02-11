package collector

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"net"
	"net/http"
	"context"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"

	"github.com/torch/config"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/torch/discovery/targetgroup"
	"github.com/torch/util/httputil"
	"github.com/torch/relabel"
)

type labelsMutator func(labels.Labels) labels.Labels

// A loop can run and be stopped again. It must not be reused after it was stopped.
type loop interface {
	run(interval, timeout time.Duration, errc chan<- error)
	stop()
}

type scrapeLoop struct {
	extractor      extractor
	l              log.Logger
	lastScrapeSize int

	sampleMutator       labelsMutator
	reportSampleMutator labelsMutator

	ctx       context.Context
	scrapeCtx context.Context
	cancel    func()
	stopped   chan struct{}

	outputDir      string
}

// scrapePool manages scrapes for sets of targets.
type scrapePool struct {
	reloadUrl  string
	outputDir  string
	logger     log.Logger

	mtx    sync.RWMutex
	config *config.ScrapeConfig
	client *http.Client
	// Targets and loops must always be synchronized to have the same
	// set of hashes.
	targets        map[uint64]*Target
	droppedTargets []*Target
	loops          map[uint64]loop
	cancel         context.CancelFunc

	// Constructor for new scrape loops. This is settable for testing convenience.
	newLoop func(*Target, extractor) loop
}

// Sync converts target groups into actual scrape targets and synchronizes
// the currently running extractor with the resulting set.
func (sp *scrapePool) Sync(tgs []*targetgroup.Group) {
	var all []*Target
	sp.mtx.Lock()
	sp.droppedTargets = []*Target{}
	for _, tg := range tgs {
		targets, err := targetsFromGroup(tg, sp.config)
		if err != nil {
			level.Error(sp.logger).Log("msg", "creating targets failed", "err", err)
			continue
		}
		for _, t := range targets {
			if t.Labels().Len() > 0 {
				all = append(all, t)
			} else if t.DiscoveredLabels().Len() > 0 {
				sp.droppedTargets = append(sp.droppedTargets, t)
			}
		}
	}
	sp.mtx.Unlock()
	sp.sync(all)
}

// sync takes a list of potentially duplicated targets, deduplicates them, starts
// scrape loops for new targets, and stops scrape loops for disappeared targets.
// It returns after all stopped scrape loops terminated.
func (sp *scrapePool) sync(targets []*Target) {
	sp.mtx.Lock()
	defer sp.mtx.Unlock()

	var (
		uniqueTargets = map[uint64]struct{}{}
		interval      = time.Duration(sp.config.ScrapeInterval)
		timeout       = time.Duration(sp.config.ScrapeTimeout)
	)

	for _, t := range targets {
		t := t
		hash := t.hash()
		uniqueTargets[hash] = struct{}{}

		if _, ok := sp.targets[hash]; !ok {
			s := &targetExtractor{
				Target: t,
				client: sp.client,
				timeout: timeout,
				logger: sp.logger,
				reloadUrl: sp.reloadUrl,
				outputDir: sp.outputDir,
			}
			l := sp.newLoop(t, s)

			sp.targets[hash] = t
			sp.loops[hash] = l

			go l.run(interval, timeout, nil)
		}
	}

	var wg sync.WaitGroup

	// Stop and remove old targets and extractor loops.
	for hash := range sp.targets {
		if _, ok := uniqueTargets[hash]; !ok {
			wg.Add(1)
			go func(l loop) {
				l.stop()
				wg.Done()
			}(sp.loops[hash])

			delete(sp.loops, hash)
			delete(sp.targets, hash)
		}
	}

	// Wait for all potentially stopped scrapers to terminate.
	// This covers the case of flapping targets. If the server is under high load, a new extractor
	// may be active and tries to insert. The old extractor that didn't terminate yet could still
	// be inserting a previous sample set.
	wg.Wait()
}

func newScrapePool(outputDir string, reloadUrl string, cfg *config.ScrapeConfig, logger log.Logger) *scrapePool {
	if logger == nil {
		logger = log.NewNopLogger()
	}

	client, err := httputil.NewClientFromConfig(cfg.HTTPClientConfig, cfg.JobName)
	if err != nil {
		// Any errors that could occur here should be caught during config validation.
		level.Error(logger).Log("msg", "Error creating HTTP client", "err", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	sp := &scrapePool{
		cancel:     cancel,
		config:     cfg,
		client:     client,
		targets:    map[uint64]*Target{},
		loops:      map[uint64]loop{},
		logger:     logger,

		reloadUrl:  reloadUrl,
		outputDir:  outputDir,
	}

	sp.newLoop = func(t *Target, s extractor) loop {
		return newScrapeLoop(
			ctx,
			s,
			log.With(logger, "target", t),
			func(l labels.Labels) labels.Labels { return sp.mutateSampleLabels(l, t) },
			func(l labels.Labels) labels.Labels { return sp.mutateReportSampleLabels(l, t) },
		)
	}

	return sp
}

func newScrapeLoop(ctx context.Context,
	sc extractor,
	l log.Logger,
	sampleMutator labelsMutator,
	reportSampleMutator labelsMutator,
) *scrapeLoop {
	if l == nil {
		l = log.NewNopLogger()
	}
	sl := &scrapeLoop{
		extractor:           sc,
		sampleMutator:       sampleMutator,
		reportSampleMutator: reportSampleMutator,
		stopped:             make(chan struct{}),
		l:                   l,
		ctx:                 ctx,
	}
	sl.scrapeCtx, sl.cancel = context.WithCancel(ctx)

	return sl
}

func (sl* scrapeLoop) run(interval, timeout time.Duration, errc chan<- error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

mainLoop:
	for {
		var (
			//start             = time.Now()
			scrapeCtx, cancel = context.WithTimeout(sl.ctx, timeout)
		)

		err := sl.extractor.extractAndSave(scrapeCtx)
		cancel()

		if err == nil {
			break mainLoop
		}

		level.Error(sl.l).Log("msg", "Extract failed", "err", err.Error())

		if errc != nil {
			errc <- err
		}

		select {
		case <-sl.ctx.Done():
			break mainLoop
		case <-sl.scrapeCtx.Done():
			break mainLoop
		case <-ticker.C:
		}
	}

	close(sl.stopped)
}

func (sl* scrapeLoop) stop() {
	sl.cancel()
	<-sl.stopped
	sl.extractor.delete()
}

func (sp *scrapePool) mutateSampleLabels(lset labels.Labels, target *Target) labels.Labels {
	lb := labels.NewBuilder(lset)

	if sp.config.HonorLabels {
		for _, l := range target.Labels() {
			if lv := lset.Get(l.Name); lv == "" {
				lb.Set(l.Name, l.Value)
			}
		}
	} else {
		for _, l := range target.Labels() {
			lv := lset.Get(l.Name)
			if lv != "" {
				lb.Set(model.ExportedLabelPrefix+l.Name, lv)
			}
			lb.Set(l.Name, l.Value)
		}
	}

	res := lb.Labels()

	return res
}

func (sp *scrapePool) mutateReportSampleLabels(lset labels.Labels, target *Target) labels.Labels {
	lb := labels.NewBuilder(lset)

	for _, l := range target.Labels() {
		lv := lset.Get(l.Name)
		if lv != "" {
			lb.Set(model.ExportedLabelPrefix+l.Name, lv)
		}
		lb.Set(l.Name, l.Value)
	}

	return lb.Labels()
}


// targetsFromGroup builds targets based on the given TargetGroup and config.
func targetsFromGroup(tg *targetgroup.Group, cfg *config.ScrapeConfig) ([]*Target, error) {
	targets := make([]*Target, 0, len(tg.Targets))

	for i, tlset := range tg.Targets {
		lbls := make([]labels.Label, 0, len(tlset)+len(tg.Labels))

		for ln, lv := range tlset {
			lbls = append(lbls, labels.Label{Name: string(ln), Value: string(lv)})
		}
		for ln, lv := range tg.Labels {
			if _, ok := tlset[ln]; !ok {
				lbls = append(lbls, labels.Label{Name: string(ln), Value: string(lv)})
			}
		}

		lset := labels.New(lbls...)

		lbls, origLabels, err := populateLabels(lset, cfg)
		if err != nil {
			return nil, fmt.Errorf("instance %d in group %s: %s", i, tg, err)
		}
		if lbls != nil || origLabels != nil {
			targets = append(targets, NewTarget(lbls, origLabels, cfg.Params))
		}
	}
	return targets, nil
}

// populateLabels builds a label set from the given label set and scrape configuration.
// It returns a label set before relabeling was applied as the second return value.
// Returns the original discovered label set found before relabelling was applied if the target is dropped during relabeling.
func populateLabels(lset labels.Labels, cfg *config.ScrapeConfig) (res, orig labels.Labels, err error) {
	// Copy labels into the labelset for the target if they are not set already.
	scrapeLabels := []labels.Label{
		{Name: model.JobLabel, Value: cfg.JobName},
		{Name: model.MetricsPathLabel, Value: cfg.MetricsPath},
		{Name: model.SchemeLabel, Value: cfg.Scheme},
	}
	lb := labels.NewBuilder(lset)

	for _, l := range scrapeLabels {
		if lv := lset.Get(l.Name); lv == "" {
			lb.Set(l.Name, l.Value)
		}
	}
	// Encode scrape query parameters as labels.
	for k, v := range cfg.Params {
		if len(v) > 0 {
			lb.Set(model.ParamLabelPrefix+k, v[0])
		}
	}

	preRelabelLabels := lb.Labels()

	lset = relabel.Process(preRelabelLabels, cfg.RelabelConfigs...)

	// Check if the target was dropped.
	if lset == nil {
		return nil, preRelabelLabels, nil
	}
	if v := lset.Get(model.AddressLabel); v == "" {
		return nil, nil, fmt.Errorf("no address")
	}

	lb = labels.NewBuilder(lset)

	// addPort checks whether we should add a default port to the address.
	// If the address is not valid, we don't append a port either.
	addPort := func(s string) bool {
		// If we can split, a port exists and we don't have to add one.
		if _, _, err := net.SplitHostPort(s); err == nil {
			return false
		}
		// If adding a port makes it valid, the previous error
		// was not due to an invalid address and we can append a port.
		_, _, err := net.SplitHostPort(s + ":1234")
		return err == nil
	}
	addr := lset.Get(model.AddressLabel)
	// If it's an address with no trailing port, infer it based on the used scheme.
	if addPort(addr) {
		// Addresses reaching this point are already wrapped in [] if necessary.
		switch lset.Get(model.SchemeLabel) {
		case "http", "":
			addr = addr + ":80"
		case "https":
			addr = addr + ":443"
		default:
			return nil, nil, fmt.Errorf("invalid scheme: %q", cfg.Scheme)
		}
		lb.Set(model.AddressLabel, addr)
	}

	//if err := config.CheckTargetAddress(model.LabelValue(addr)); err != nil {
	//	return nil, nil, err
	//}

	// Meta labels are deleted after relabelling. Other internal labels propagate to
	// the target which decides whether they will be part of their label set.
	for _, l := range lset {
		if strings.HasPrefix(l.Name, model.MetaLabelPrefix) {
			lb.Del(l.Name)
		}
	}

	// Default the instance label to the target address.
	if v := lset.Get(model.InstanceLabel); v == "" {
		lb.Set(model.InstanceLabel, addr)
	}

	res = lb.Labels()
	for _, l := range res {
		// Check label values are valid, drop the target if not.
		if !model.LabelValue(l.Value).IsValid() {
			return nil, nil, fmt.Errorf("invalid label value for %q: %q", l.Name, l.Value)
		}
	}
	return res, preRelabelLabels, nil
}
