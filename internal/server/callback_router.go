package server

import "fmt"

// matchedRule pairs a handler with its async flag from the matching rule.
type matchedRule struct {
	handler CallbackHandler
	async   bool
}

// routerRule is an internal representation of a callback routing rule.
type routerRule struct {
	events  []string
	handler CallbackHandler
	async   bool
}

// CallbackRouter matches callback events to their configured handlers.
type CallbackRouter struct {
	rules []routerRule
}

// NewCallbackRouter creates a CallbackRouter from config rules and a handler registry.
// handlerRegistry maps rule index to a CallbackHandler instance.
func NewCallbackRouter(events [][]string, asyncFlags []bool, handlers map[int]CallbackHandler) (*CallbackRouter, error) {
	if len(events) != len(asyncFlags) {
		return nil, fmt.Errorf("callback router: events and asyncFlags length mismatch")
	}

	rules := make([]routerRule, 0, len(events))
	for i, evts := range events {
		h, ok := handlers[i]
		if !ok {
			return nil, fmt.Errorf("callback router: no handler for rule %d", i)
		}
		if len(evts) == 0 {
			return nil, fmt.Errorf("callback router: rule %d has no events", i)
		}
		rules = append(rules, routerRule{
			events:  evts,
			handler: h,
			async:   asyncFlags[i],
		})
	}

	return &CallbackRouter{rules: rules}, nil
}
