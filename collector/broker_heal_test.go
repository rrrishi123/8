package main

import (
	"reflect"
	"testing"
)

func TestListenerPIDsNeverSelf(t *testing.T) {
	// the live shape from 2026-11-01: the collector's own client socket + the channel listener
	got := listenerPIDs("77739\n81582\n", 77739)
	if !reflect.DeepEqual(got, []int{81582}) {
		t.Fatalf("self must be dropped: got %v", got)
	}
	if got := listenerPIDs("", 1); got != nil {
		t.Fatalf("empty output must yield nil, got %v", got)
	}
	if got := listenerPIDs("abc\n0\n1\n-5\n4242\n", 9); !reflect.DeepEqual(got, []int{4242}) {
		t.Fatalf("garbage/pid<=1 must be dropped: got %v", got)
	}
}
