package ui

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// historyStep is the sampling interval of the pool hashrate.
const historyStep = 30 * time.Second

// Point is one hashrate sample (unix seconds, H/s).
type Point struct {
	T  int64   `json:"t"`
	HR float64 `json:"hr"`
}

// tier is a fixed-size series at one resolution: samples are averaged
// into buckets of step.
type tier struct {
	Step   time.Duration `json:"step"`
	Max    int           `json:"max"`
	Points []Point       `json:"points"`
	accSum float64
	accN   int
	accT   int64
}

func (t *tier) add(ts int64, hr float64) {
	b := ts - ts%int64(t.Step.Seconds())
	if t.accN > 0 && b != t.accT {
		t.Points = append(t.Points, Point{T: t.accT, HR: t.accSum / float64(t.accN)})
		if len(t.Points) > t.Max {
			t.Points = t.Points[len(t.Points)-t.Max:]
		}
		t.accSum, t.accN = 0, 0
	}
	t.accT = b
	t.accSum += hr
	t.accN++
}

// history keeps 1 h at 30 s, 24 h at 5 min and 7 d at 30 min.
type history struct {
	mu    sync.Mutex
	Tiers map[string]*tier `json:"tiers"`
}

func newHistory() *history {
	return &history{Tiers: map[string]*tier{
		"1h":  {Step: historyStep, Max: 120},
		"24h": {Step: 5 * time.Minute, Max: 288},
		"7d":  {Step: 30 * time.Minute, Max: 336},
	}}
}

func (h *history) add(now time.Time, hr float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, t := range h.Tiers {
		t.add(now.Unix(), hr)
	}
}

// series returns a copy of the points for a range, including the bucket in
// progress.
func (h *history) series(rng string) ([]Point, time.Duration, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.Tiers[rng]
	if !ok {
		return nil, 0, false
	}
	out := append([]Point{}, t.Points...)
	if t.accN > 0 {
		out = append(out, Point{T: t.accT, HR: t.accSum / float64(t.accN)})
	}
	return out, t.Step, true
}

// average returns the mean hashrate over the last window and how many
// seconds of samples it is based on.
func (h *history) average(window time.Duration) (float64, float64) {
	pts, step, _ := h.series("24h")
	cut := time.Now().Add(-window).Unix()
	sum, n := 0.0, 0
	for _, p := range pts {
		if p.T >= cut {
			sum += p.HR
			n++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return sum / float64(n), float64(n) * step.Seconds()
}

func (h *history) load(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var saved map[string][]Point
	if json.Unmarshal(b, &saved) != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for k, pts := range saved {
		if t := h.Tiers[k]; t != nil {
			if len(pts) > t.Max {
				pts = pts[len(pts)-t.Max:]
			}
			t.Points = pts
		}
	}
}

func (h *history) save(path string) error {
	h.mu.Lock()
	out := map[string][]Point{}
	for k, t := range h.Tiers {
		out[k] = append([]Point{}, t.Points...)
	}
	h.mu.Unlock()
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
