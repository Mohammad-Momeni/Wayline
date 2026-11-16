package main

import (
	"context"
	"fmt"
	"net"
	"sync"
)

// Each managed hop receives an upstream listener backed by the preceding
// chain. Its local listener becomes the base for subsequent hops.
func prepareChain(parent context.Context, base, direct Dial, hops []Hop) (Dial, func(), <-chan error, error) {
	ctx, cancel := context.WithCancel(parent)
	var stops []func()
	var workers sync.WaitGroup
	failures := make(chan error, len(hops)+1)
	stop := func() {
		cancel()
		for i := len(stops) - 1; i >= 0; i-- {
			stops[i]()
		}
		workers.Wait()
	}
	current := base
	for _, hop := range activeHops(hops) {
		if hop.Type != "xray" {
			current = chain(current, []Hop{hop})
			continue
		}
		if hop.Xray == nil {
			stop()
			return nil, nil, nil, permanentError{fmt.Errorf("select a saved Xray configuration")}
		}
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			stop()
			return nil, nil, nil, err
		}
		stops = append(stops, func() { listener.Close() })
		proxy := &ProxyServer{dial: current}
		workers.Add(1)
		go func() { defer workers.Done(); proxy.Serve(ctx, listener) }()
		local, stopXray, exited, err := startXray(ctx, *hop.Xray, listener.Addr().(*net.TCPAddr).Port)
		if err != nil {
			stop()
			return nil, nil, nil, permanentError{err}
		}
		stops = append(stops, stopXray)
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case err := <-exited:
				select {
				case failures <- err:
				case <-ctx.Done():
				}
			case <-ctx.Done():
			}
		}()
		current = chain(direct, []Hop{local})
	}
	return current, stop, failures, nil
}
