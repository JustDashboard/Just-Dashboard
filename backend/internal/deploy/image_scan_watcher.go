package deploy

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/imagescan"
)

// ImageScanner is the scanner as the watch uses it: internal/imagescan's,
// or a test's.
type ImageScanner interface {
	Scan(ctx context.Context, ref string, pull bool) (*imagescan.Scanned, error)
}

// ImageScanWatcher runs the scans: the one after each build, the one a
// person asks for, and each live release's daily one, one at a time. It is a
// run observer, which is how it hears that a build made a release.
type ImageScanWatcher struct {
	store    *OrchestrationStore
	scanner  ImageScanner
	resolver ImageDigestResolver
	log      *slog.Logger
	interval time.Duration
	wake     chan struct{}
}

// NewImageScanWatcher is a watch over store's releases. A nil scanner is a
// host without Docker: nothing is scanned, and the panel says why.
func NewImageScanWatcher(store *OrchestrationStore, scanner ImageScanner, resolver ImageDigestResolver, log *slog.Logger) *ImageScanWatcher {
	return &ImageScanWatcher{
		store: store, scanner: scanner, resolver: resolver, log: log,
		interval: 5 * time.Minute, wake: make(chan struct{}, 1),
	}
}

// Available says there is a scanner to run.
func (w *ImageScanWatcher) Available() bool { return w != nil && w.scanner != nil }

// Start runs the watch until ctx ends. A scan the last process was running
// is asked for again first.
func (w *ImageScanWatcher) Start(ctx context.Context) {
	if !w.Available() {
		return
	}
	if err := w.store.RequeueInterruptedImageScans(ctx); err != nil {
		w.warn("interrupted image scans could not be requeued", err)
	}
	go func() {
		// The first look waits a minute: a start-up is busy enough without
		// a scan of every live release joining it.
		timer := time.NewTimer(time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			case <-w.wake:
			}
			for ctx.Err() == nil {
				releaseID, trigger, err := w.store.ClaimImageScan(ctx)
				if err != nil {
					w.warn("the next image scan could not be read", err)
					break
				}
				if releaseID == 0 {
					break
				}
				if err := w.ScanRelease(ctx, releaseID, trigger); err != nil && ctx.Err() == nil {
					w.warn("an image scan was not recorded", err)
				}
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(w.interval)
		}
	}()
}

// Notify wakes the watch for a scan just asked for.
func (w *ImageScanWatcher) Notify() {
	if w == nil {
		return
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *ImageScanWatcher) RunStarted(context.Context, EngineRun) {}

// RunFinished asks for a scan of the release a run made live, once: a
// rollback or a restart of a release already scanned asks for nothing.
func (w *ImageScanWatcher) RunFinished(ctx context.Context, run EngineRun) {
	if !w.Available() || run.State != RunSucceeded || run.ReleaseID == 0 {
		return
	}
	existing, err := w.store.ImageScan(ctx, run.ReleaseID)
	if err != nil || existing != nil {
		return
	}
	if _, err := w.store.QueueImageScan(ctx, run.ReleaseID, ImageScanTriggerBuild); err != nil {
		w.warn("the scan after a build could not be asked for", err)
		return
	}
	w.Notify()
}

// ScanRelease scans one release's images now and keeps the result. The
// bases it was pinned on are read after a build and when a person asks; the
// daily scan carries their findings forward, since a base is pinned by
// digest and only the database it is read against moves.
func (w *ImageScanWatcher) ScanRelease(ctx context.Context, releaseID int64, trigger string) error {
	if !w.Available() {
		return errors.New("there is no scanner on this host")
	}
	images, bases, err := w.store.ImageScanTargets(ctx, releaseID)
	if err != nil {
		return w.store.FinishImageScan(context.WithoutCancel(ctx), releaseID, ImageScanResult{
			State: ImageScanFailed, Reason: "The release's images could not be read: " + err.Error(),
		})
	}
	previous, _ := w.store.ImageScan(ctx, releaseID)
	result := ImageScanResult{State: ImageScanFailed, Findings: []imagescan.Finding{}, Targets: []ImageScanTarget{}}
	var found []imagescan.Finding
	reasons := []string{}
	for _, image := range images {
		target, findings, scanned := w.scanTarget(ctx, image, false, &result)
		if !scanned {
			reasons = append(reasons, target.Reason)
		} else {
			result.State = ImageScanScanned
		}
		if image.Service != "" {
			for i := range findings {
				findings[i].Target = image.Service + " · " + findings[i].Target
			}
		}
		found = append(found, findings...)
		result.Targets = append(result.Targets, target)
	}
	if len(images) == 0 {
		reasons = append(reasons, "This release has no image on this server to scan.")
	}
	var baseFindings [][]imagescan.Finding
	readBases := trigger != ImageScanTriggerSchedule || previous == nil
	for _, base := range bases {
		var target ImageScanTarget
		if !readBases {
			if carried, ok := carriedBase(previous, base); ok {
				target = carried
			} else {
				target, _, _ = w.scanTarget(ctx, base, true, &result)
			}
		} else {
			target, _, _ = w.scanTarget(ctx, base, true, &result)
		}
		baseFindings = append(baseFindings, target.Findings())
		result.Targets = append(result.Targets, target)
	}
	result.Findings = imagescan.MarkFromBase(found, baseFindings...)
	if result.State != ImageScanScanned {
		result.Reason = firstReason(reasons)
	}
	if updates, err := w.store.ReleaseBaseUpdates(ctx, w.resolver, releaseID); err == nil {
		result.BaseUpdates = updates
	} else {
		result.BaseUpdates = []ReleaseBaseUpdate{}
	}
	return w.store.FinishImageScan(context.WithoutCancel(ctx), releaseID, result)
}

func (w *ImageScanWatcher) scanTarget(ctx context.Context, image ImageScanImage, pull bool, result *ImageScanResult) (ImageScanTarget, []imagescan.Finding, bool) {
	target := ImageScanTarget{Kind: image.Kind, Service: image.Service, Reference: image.Reference, Digest: image.Digest, State: ImageScanFailed}
	scanned, err := w.scanner.Scan(ctx, image.Ref, pull)
	if err != nil {
		target.Reason = clipText(err.Error(), 500)
		return target, nil, false
	}
	at := w.store.now().UTC()
	target.State, target.ScannedAt = ImageScanScanned, &at
	target.OS, target.EndOfLife = scanned.Report.OS, scanned.Report.EndOfLife
	target.Counts = imagescan.Summarize(scanned.Report.Findings, imagescan.SeverityLow).Counts
	if result.Scanner == "" {
		result.Scanner, result.Database = scanned.Report.Scanner, scanned.Database
	}
	return target.WithFindings(scanned.Report.Findings), scanned.Report.Findings, true
}

// carriedBase is the previous scan's reading of a base at the same digest.
func carriedBase(previous *ImageScan, base ImageScanImage) (ImageScanTarget, bool) {
	if previous == nil {
		return ImageScanTarget{}, false
	}
	for _, target := range previous.Targets {
		if target.Kind == "base" && target.Digest == base.Digest && target.State == ImageScanScanned {
			return target, true
		}
	}
	return ImageScanTarget{}, false
}

func firstReason(reasons []string) string {
	for _, reason := range reasons {
		if reason != "" {
			return reason
		}
	}
	return "Nothing could be scanned."
}

func (w *ImageScanWatcher) warn(message string, err error) {
	if w.log != nil {
		w.log.Warn(message, "err", err)
	}
}
