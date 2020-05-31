package dynconf

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

type retry struct {
	MaxNumberOfAttempts int
	MinBackoff          time.Duration
	MaxBackoff          time.Duration
	BackoffFactor       float64
	BackoffJitter       float64

	normalizeOnce sync.Once
}

func (r *retry) Do(ctx context.Context, callback func() bool) (bool, error) {
	r.normalize()
	attemptCount := 0
	backoff := time.Duration(0)
	rand := rand.New(rand.NewSource(time.Now().UnixNano()))

	for {
		if callback() {
			return true, nil
		}

		attemptCount++

		if attemptCount == r.MaxNumberOfAttempts {
			return false, nil
		}

		if backoff == 0 {
			backoff = r.MinBackoff
		} else {
			backoff = time.Duration(float64(backoff) * r.BackoffFactor)

			if backoff > r.MaxBackoff {
				backoff = r.MaxBackoff
			}
		}

		p := (1.0 - r.BackoffJitter) + (2*r.BackoffJitter)*rand.Float64()
		timer := time.NewTimer(time.Duration(float64(backoff) * p))

		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		}
	}
}

func (r *retry) normalize() {
	r.normalizeOnce.Do(func() {
		if r.MinBackoff < 1 {
			r.MinBackoff = 100 * time.Millisecond
		}

		if r.MaxBackoff < 1 {
			r.MaxBackoff = 300 * time.Second
		}

		if r.MaxBackoff < r.MinBackoff {
			r.MaxBackoff = r.MinBackoff
		}

		if r.BackoffFactor < 1.0 {
			r.BackoffFactor = 2.0
		}

		if r.BackoffJitter < 0.0 {
			r.BackoffJitter = 0.0
		}

		if r.BackoffJitter > 1.0 {
			r.BackoffJitter = 1.0
		}
	})
}
