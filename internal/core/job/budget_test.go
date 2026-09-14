package job_test

import (
	"context"
	"testing"

	"github.com/gsoares85/hermes/internal/core/job"
)

// What reporting costs, measured rather than asserted.
//
// The package says progress is "cheap enough to do per row", and a COPY of a
// million rows is what that claim is for. It is a number rather than a budget
// with a threshold because the threshold that matters is not the one a runner
// can measure: what would hurt is reporting serialising against the rest of
// the queue, and two jobs reporting while the panel samples is the shape of
// that, not a nanosecond count on somebody's laptop.
//
// The parallel case is the one to read. If it is far worse per operation than
// the sequential one, reporting has gone back behind a lock everything else
// is read through.
func BenchmarkReportingProgress(b *testing.B) {
	reporters := make(chan job.Reporter, 1)

	queue := job.NewQueue()
	if _, err := queue.Submit(job.Spec{Kind: "bench"}, runnerFunc(
		func(ctx context.Context, report job.Reporter) error {
			reporters <- report
			<-ctx.Done()

			return ctx.Err()
		})); err != nil {
		b.Fatalf("Submit(...) = _, %v, want no error", err)
	}

	report := <-reporters

	b.Run("one job", func(b *testing.B) {
		for i := range b.N {
			report.Report(job.Progress{Step: "copying public.orders", Unit: "rows", Done: int64(i)})
		}
	})

	b.Run("every job at once", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				report.Report(job.Progress{Step: "copying public.orders", Unit: "rows"})
			}
		})
	})

	// And what the window costs while that is going on: the sample the panel
	// takes ten times a second reads every job the queue holds.
	b.Run("the window looking on", func(b *testing.B) {
		for range b.N {
			_ = queue.List()
		}
	})
}
