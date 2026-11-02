package history

import (
	"context"
	"errors"
	"fmt"

	"github.com/metril/wattson/internal/store"
)

// Recover closes rides left open by a crash: those with fewer than
// minSamples samples are deleted, the rest finished at their last sample.
// onFinished (may be nil) is called for each ride finished this way, like the
// recorder's OnFinished. A ride that cannot be settled is skipped and
// reported in the error.
func Recover(ctx context.Context, st *store.Store, minSamples int, onFinished func(context.Context, store.Ride)) (finished, deleted int, err error) {
	open, err := st.OpenRides(ctx)
	if err != nil {
		return 0, 0, err
	}
	var errs []error
	for _, r := range open {
		ss, err := st.Samples(ctx, r.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("ride %d: %w", r.ID, err))
			continue
		}
		if len(ss) < minSamples {
			if err := st.DeleteRide(ctx, r.ID); err != nil {
				errs = append(errs, fmt.Errorf("ride %d: %w", r.ID, err))
				continue
			}
			deleted++
			continue
		}
		fin, err := st.FinishRide(ctx, r.ID, ss[len(ss)-1].At, Summarize(ss), len(ss))
		if err != nil {
			errs = append(errs, fmt.Errorf("ride %d: %w", r.ID, err))
			continue
		}
		finished++
		if onFinished != nil {
			onFinished(ctx, fin)
		}
	}
	return finished, deleted, errors.Join(errs...)
}
