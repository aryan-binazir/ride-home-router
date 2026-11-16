package orderedroute

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

func (c *Client) measureBeforeBatching(ctx context.Context, requests []Request) []Result {
	results := make([]Result, len(requests))
	for i := range requests {
		results[i].ID = requests[i].ID
	}
	failAll := func(err error) []Result {
		for i := range results {
			results[i].Err = err
		}
		return results
	}
	if len(requests) == 0 {
		return results
	}
	key, err := c.currentKey(ctx)
	if err != nil {
		return failAll(err)
	}
	for _, request := range requests {
		if len(request.Points) < 2 {
			return failAll(errors.New("orderedroute: a route needs at least an origin and a destination"))
		}
	}

	groups := make(map[string][]int)
	var order []string
	for i, request := range requests {
		group := request.Group
		if group == "" {
			group = request.ID
		}
		if _, seen := groups[group]; !seen {
			order = append(order, group)
		}
		groups[group] = append(groups[group], i)
	}
	var wg sync.WaitGroup
	for _, group := range order {
		indexes := groups[group]
		wg.Go(func() {
			attempts := 0
			for _, index := range indexes {
				attempts += RequestsFor(len(requests[index].Points))
			}
			if err := c.reserve(ctx, attempts); err != nil {
				for _, index := range indexes {
					results[index].Err = fmt.Errorf("orderedroute: %w", err)
				}
				return
			}
			var inner sync.WaitGroup
			for _, index := range indexes {
				inner.Go(func() {
					legs, err := c.measureRoute(ctx, key, requests[index].Points)
					results[index].Legs, results[index].Err = legs, err
				})
			}
			inner.Wait()
		})
	}
	wg.Wait()
	return results
}
