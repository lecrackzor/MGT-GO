package coordinator

import (
	"encoding/json"
	"testing"
	"time"

	"market-terminal/internal/api"
)

func TestAggregateKeepsValueWhenLaterEndpointIsBlank(t *testing.T) {
	dcc := &DataCollectionCoordinator{}
	first := api.Query{Ticker: "SPX", Endpoint: "classic_zero"}
	second := api.Query{Ticker: "SPX", Endpoint: "classic_full"}
	results := map[api.Query]map[string]interface{}{
		first: {
			"spot":       10.0,
			"timestamp":  100.0,
			"zero_gamma": nil,
		},
		second: {
			"spot":       nil,
			"timestamp":  1.7e12,
			"zero_gamma": 3.0,
			"ladder":     json.RawMessage(`null`),
		},
	}

	got := dcc.aggregateResults([]api.Query{first, second}, results, nil)["SPX"]
	if got["spot"] != 10.0 {
		t.Fatalf("spot wiped by later null: %#v", got["spot"])
	}
	if got["zero_gamma"] != 3.0 {
		t.Fatalf("zero_gamma = %#v", got["zero_gamma"])
	}
	if _, ok := got["ladder"]; ok {
		t.Fatal("null raw profile was stored")
	}
	if got["timestamp"] != 1.7e9 {
		t.Fatalf("timestamp = %#v, want newest seconds", got["timestamp"])
	}
}

func TestAggregateNamespacesProfileLadders(t *testing.T) {
	dcc := &DataCollectionCoordinator{}
	zero := api.Query{Ticker: "SPX", Endpoint: "classic_zero"}
	one := api.Query{Ticker: "SPX", Endpoint: "classic_one"}
	results := map[api.Query]map[string]interface{}{
		zero: {
			"spot":    10.0,
			"strikes": json.RawMessage(`[[1,2]]`),
		},
		one: {
			"spot":    nil,
			"strikes": json.RawMessage(`[[3,4]]`),
		},
	}

	got := dcc.aggregateResults([]api.Query{zero, one}, results, nil)["SPX"]
	if _, ok := got["strikes"]; ok {
		t.Fatal("strikes was flattened onto the row")
	}
	if got["spot"] != 10.0 {
		t.Fatalf("spot = %#v", got["spot"])
	}
	profs, _ := got["profiles"].(map[string]interface{})
	if profs == nil {
		t.Fatal("profiles missing")
	}
	zeroPayload, _ := profs["classic_zero"].(map[string]interface{})
	onePayload, _ := profs["classic_one"].(map[string]interface{})
	if string(zeroPayload["strikes"].(json.RawMessage)) != `[[1,2]]` {
		t.Fatalf("classic_zero strikes = %#v", zeroPayload["strikes"])
	}
	if string(onePayload["strikes"].(json.RawMessage)) != `[[3,4]]` {
		t.Fatalf("classic_one strikes = %#v", onePayload["strikes"])
	}
	if zeroPayload["spot"] != 10.0 {
		t.Fatalf("classic_zero spot = %#v", zeroPayload["spot"])
	}
}

func TestStateAndLaterGreeksDoNotReplaceChartLevels(t *testing.T) {
	dcc := &DataCollectionCoordinator{}
	classic := api.Query{Ticker: "ES_SPX", Endpoint: "classic_zero"}
	state := api.Query{Ticker: "ES_SPX", Endpoint: "state_zero"}
	gamma := api.Query{Ticker: "ES_SPX", Endpoint: "gamma_zero"}
	charm := api.Query{Ticker: "ES_SPX", Endpoint: "charm_one"}
	results := map[api.Query]map[string]interface{}{
		classic: {
			"spot":        7733.0,
			"zero_gamma":  7741.0,
			"sum_gex_vol": -12574.0,
			"strikes":     json.RawMessage(`[[1]]`),
		},
		state: {
			"spot":        7690.0,
			"zero_gamma":  0.0,
			"sum_gex_vol": -398.0,
			"strikes":     json.RawMessage(`[[2]]`),
		},
		gamma: {
			"major_long_gamma": 7767.0,
			"mini_contracts":   json.RawMessage(`[[3]]`),
		},
		charm: {
			"major_long_gamma": 7542.0,
			"mini_contracts":   json.RawMessage(`[[4]]`),
		},
	}
	order := []api.Query{classic, state, gamma, charm}
	got := dcc.aggregateResults(order, results, nil)["ES_SPX"]
	if got["zero_gamma"] != 7741.0 {
		t.Fatalf("zero_gamma = %#v", got["zero_gamma"])
	}
	if got["sum_gex_vol"] != -12574.0 {
		t.Fatalf("sum_gex_vol = %#v", got["sum_gex_vol"])
	}
	if got["major_long_gamma"] != 7767.0 {
		t.Fatalf("major_long_gamma = %#v", got["major_long_gamma"])
	}
	if got["spot"] != 7733.0 {
		t.Fatalf("spot = %#v", got["spot"])
	}
	profs, _ := got["profiles"].(map[string]interface{})
	statePayload, _ := profs["state_zero"].(map[string]interface{})
	if statePayload["zero_gamma"] != 0.0 {
		t.Fatalf("state ladder lost its own zero_gamma: %#v", statePayload["zero_gamma"])
	}
	if statePayload["spot"] != 7690.0 {
		t.Fatalf("state ladder lost its own spot: %#v", statePayload["spot"])
	}
}

func TestStopDateRolloverMonitorReturns(t *testing.T) {
	dcc := NewDataCollectionCoordinator(
		nil, nil, nil, nil,
		func() bool { return false },
		func() []interface{} { return nil },
		func(string, string) {},
	)
	dcc.StartDateRolloverMonitor()
	done := make(chan struct{})
	go func() {
		dcc.StopDateRolloverMonitor()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StopDateRolloverMonitor did not return")
	}
}
