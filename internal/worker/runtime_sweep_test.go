package worker

import (
	"testing"

	"mp_sched/internal/model"
)

func TestEffectiveMaxRuntimeSec(t *testing.T) {
	def := 3600
	cases := []struct {
		task *model.Task
		want int
	}{
		{&model.Task{MaxRuntimeSeconds: 0}, 3600},
		{&model.Task{MaxRuntimeSeconds: 120}, 120},
		{&model.Task{MaxRuntimeSeconds: -1}, -1},
		{nil, 3600},
	}
	for _, tc := range cases {
		if g := effectiveMaxRuntimeSec(tc.task, def); g != tc.want {
			t.Fatalf("effectiveMaxRuntimeSec(%v, %d)=%d want %d", tc.task, def, g, tc.want)
		}
	}
}
