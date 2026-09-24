package cli

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/Bethel-nz/pier/internal/capture"
	"github.com/Bethel-nz/pier/internal/replay"
)

func newReplayCommand(rt *runtime) *cobra.Command {
	var service string
	var since time.Duration
	var limit int
	var show bool
	cmd := &cobra.Command{
		Use:   "replay [id...]",
		Short: "List captured requests, or send them to their service again",
		Long: "Without arguments, lists the newest captured requests (services with capture: in pier.yaml).\n" +
			"With ids, sends those requests to the service again and compares the answers.\n" +
			"--since sends every request captured in that window, oldest first. --show prints requests instead of sending them.",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := rt.renderer("replay")
			proj, targets, err := rt.app.Targets(rt.start())
			if err != nil {
				return out.Error(err)
			}
			store, err := capture.OpenExisting(capture.PathFor(proj.Root))
			if errors.Is(err, capture.ErrNoCaptures) {
				return out.Error(errors.New("Pier has no captured requests for this project; add capture: 24h to a service in pier.yaml and run pier up"))
			}
			if err != nil {
				return out.Error(fmt.Errorf("Pier could not open captured requests: %w", err))
			}
			defer store.Close()
			ctx := cmd.Context()

			var picked []capture.Exchange
			switch {
			case len(args) > 0:
				for _, arg := range args {
					id, err := strconv.ParseInt(arg, 10, 64)
					if err != nil {
						return out.Error(fmt.Errorf("%q is not a request id; run pier replay to list them", arg))
					}
					exchange, err := store.Get(ctx, id)
					if err != nil {
						return out.Error(err)
					}
					picked = append(picked, exchange)
				}
			case since > 0:
				if picked, err = store.List(ctx, capture.Query{Service: service, Since: time.Now().Add(-since), Oldest: true}); err != nil {
					return out.Error(err)
				}
				if len(picked) == 0 {
					return out.Error(fmt.Errorf("Pier captured no requests in the last %s", since))
				}
			default:
				listed, err := store.List(ctx, capture.Query{Service: service, Limit: limit})
				if err != nil {
					return out.Error(err)
				}
				return out.ReplayList(proj, listed)
			}

			if show {
				return out.ReplayShow(proj, picked)
			}
			client := replay.Client()
			results := make([]replay.Result, 0, len(picked))
			for _, exchange := range picked {
				target, ok := targets[exchange.Service]
				if !ok {
					results = append(results, replay.Result{Exchange: exchange, Err: fmt.Errorf("service %q is no longer in pier.yaml", exchange.Service)})
					continue
				}
				results = append(results, replay.Send(ctx, client, target, exchange))
			}
			return out.ReplayResults(proj, results)
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "only this service's requests")
	cmd.Flags().DurationVar(&since, "since", 0, "send every request captured in this window, such as 10m")
	cmd.Flags().IntVar(&limit, "limit", 20, "how many requests to list")
	cmd.Flags().BoolVar(&show, "show", false, "print the requests and their answers instead of sending them")
	return cmd
}
