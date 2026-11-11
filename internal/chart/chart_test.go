package chart

import (
	"reflect"
	"testing"
	"time"
)

func fp(v float64) *float64 { return &v }

func testDays(n int) []time.Time {
	days := make([]time.Time, n)
	for i := range days {
		days[i] = time.Date(2026, 11, 1+i, 0, 0, 0, 0, time.UTC)
	}
	return days
}

func TestNiceTicks(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		top, minStep float64
		want         []float64
	}{
		{1, 1, []float64{0, 1}},
		{3, 1, []float64{0, 1, 2, 3}},
		{7, 1, []float64{0, 2, 4, 6, 8}},
		{812, 1, []float64{0, 500, 1000}},
		{100, 1, []float64{0, 50, 100}},
		{0, 1, []float64{0, 1}},
	} {
		if got := niceTicks(tt.top, tt.minStep); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("niceTicks(%v) = %v, want %v", tt.top, got, tt.want)
		}
	}
}

func TestDurationTicks(t *testing.T) {
	t.Parallel()
	for top, want := range map[float64][]float64{
		45:    {0, 20, 40, 60},
		300:   {0, 120, 240, 360},
		10000: {0, 3600, 7200, 10800},
	} {
		if got := durationTicks(top); !reflect.DeepEqual(got, want) {
			t.Errorf("durationTicks(%v) = %v, want %v", top, got, want)
		}
	}
}

func TestFormats(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ got, want string }{
		{formatMillis(0), "0"},
		{formatMillis(812.4), "812 ms"},
		{formatMillis(1500), "1.5 s"},
		{formatDuration(0), "0"},
		{formatDuration(45), "45s"},
		{formatDuration(252), "4m 12s"},
		{formatDuration(300), "5m"},
		{formatDuration(3600), "1h"},
		{formatDuration(5400), "1h 30m"},
		{compactCount(9999), "9,999"},
		{compactCount(12500), "12.5K"},
		{compactCount(2_000_000), "2M"},
	} {
		if tt.got != tt.want {
			t.Errorf("got %q, want %q", tt.got, tt.want)
		}
	}
}

func TestAxisLabels(t *testing.T) {
	t.Parallel()
	for n, want := range map[int][]string{
		1:  {"Nov 1"},
		2:  {"Nov 1", "Nov 2"},
		7:  {"Nov 1", "Nov 3", "Nov 5", "Nov 7"},
		30: {"Nov 1", "Nov 11", "Nov 21", "Nov 30"},
	} {
		var got []string
		labels := axisLabels(testDays(n))
		for _, l := range labels {
			got = append(got, l.Text)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%d days: %v, want %v", n, got, want)
		}
	}
	labels := axisLabels(testDays(30))
	if labels[0].Anchor != "start" || labels[0].X != "0%" || labels[3].Anchor != "end" || labels[3].X != "100%" {
		t.Errorf("edge labels aren't kept inside the plot: %+v", labels)
	}
}

func lineChartOf(values ...*float64) Data {
	return Data{
		Kind: Lines, Days: testDays(len(values)), Format: MillisFormat,
		Series: []Series{WithValues(Slot1, "p50", values)},
	}
}

func TestLineBreaksAtGaps(t *testing.T) {
	t.Parallel()
	v := layout(lineChartOf(fp(1), fp(2), nil, fp(3), nil, nil, nil, nil, fp(4), fp(5)))
	l := v.Lines[0]
	if len(l.Segments) != 2 {
		t.Errorf("segments = %d, want 2 (no line across a gap)", len(l.Segments))
	}
	// The lone point at index 3 and the last point at index 9.
	if len(l.Dots) != 2 || l.Dots[0].X != "35.000%" || l.Dots[1].X != "95.000%" {
		t.Errorf("dots = %+v", l.Dots)
	}
	if v.Rows[2].Values[0] != "–" {
		t.Errorf("gap in table = %q", v.Rows[2].Values[0])
	}
}

func TestFormatNumber(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		v    float64
		want string
	}{
		{0, "0"},
		{0.004, "0.004"},
		{0.123456, "0.1235"},
		{1.5, "1.5"},
		{547, "547"},
		{999.96, "1000"},
		{1234.5, "1,235"},
		{-1234.5, "-1,235"},
		{-5, "-5"},
		{1e30, "1e+30"},
		{-1e30, "-1e+30"},
	} {
		if got := formatNumber(tt.v); got != tt.want {
			t.Errorf("formatNumber(%v) = %q, want %q", tt.v, got, tt.want)
		}
	}
}

func TestLayoutValuesNotAboveZero(t *testing.T) {
	t.Parallel()
	zeros := Data{Kind: Lines, Days: testDays(2), Format: NumberFormat, Series: []Series{{Values: []*float64{fp(0), fp(0)}}}}
	if layout(zeros).HasData {
		t.Error("all zeros plotted without PlotZeros")
	}
	zeros.PlotZeros = true
	if v := layout(zeros); !v.HasData || len(v.Ticks) < 2 {
		t.Errorf("all zeros with PlotZeros = %+v", v)
	}

	for _, values := range [][]*float64{
		{fp(-5), fp(-20)},
		{fp(-5), fp(20)},
		{fp(-1e308), fp(1e308)},
		{fp(0.001), fp(0.004)},
	} {
		v := layout(Data{Kind: Lines, Days: testDays(2), Format: NumberFormat, Series: []Series{{Values: values}}})
		if !v.HasData {
			t.Errorf("%v has no data", values)
			continue
		}
		for _, tick := range v.Ticks {
			if !(tick.Y >= plotTop-1e-6 && tick.Y <= plotBase+1e-6) {
				t.Errorf("%v: tick %q at %v is outside the plot", values, tick.Label, tick.Y)
			}
		}
		for _, line := range v.Lines {
			for _, seg := range line.Segments {
				for _, y := range []float64{seg.Y1, seg.Y2} {
					if !(y >= plotTop-1e-6 && y <= plotBase+1e-6) {
						t.Errorf("%v: point at %v is outside the plot", values, y)
					}
				}
			}
		}
	}
}

func TestLayoutEmpty(t *testing.T) {
	t.Parallel()
	if v := layout(lineChartOf(nil, nil)); v.HasData || len(v.Ticks) != 0 {
		t.Errorf("no values: HasData %v, ticks %v", v.HasData, v.Ticks)
	}
	zero := Data{
		Kind: Columns, Days: testDays(2), Format: CountFormat,
		Series: []Series{WithValues(Slot1, "Opens", []*float64{fp(0), fp(0)})},
	}
	if layout(zero).HasData {
		t.Error("all zero: HasData")
	}
}

func TestColumns(t *testing.T) {
	t.Parallel()
	values := []*float64{fp(100), fp(0), fp(1)}
	v := layout(Data{
		Kind: Columns, Days: testDays(3), Format: CountFormat,
		Series: []Series{WithValues(Slot1, "Opens", values)},
	})
	if len(v.Bars) != 2 {
		t.Fatalf("bars = %d, want 2 (zero draws nothing)", len(v.Bars))
	}
	if b := v.Bars[0]; b.Y != plotTop || b.Y+b.Height != plotBase+barOverhang {
		t.Errorf("tallest bar = %+v", b)
	}
	if b := v.Bars[1]; b.Height-barOverhang != 2 {
		t.Errorf("small bar height = %v, want the 2px minimum", b.Height-barOverhang)
	}
	if v.BarWidth != "20.00" {
		t.Errorf("BarWidth = %q", v.BarWidth)
	}
	many := make([]*float64, 90)
	for i := range many {
		many[i] = fp(5)
	}
	w := layout(Data{
		Kind: Columns, Days: testDays(90), Format: CountFormat,
		Series: []Series{WithValues(Slot1, "Opens", many)},
	})
	if w.BarWidth != "0.778%" {
		t.Errorf("90 day BarWidth = %q", w.BarWidth)
	}
}

func TestTooltipStaysInPlot(t *testing.T) {
	t.Parallel()
	v := layout(lineChartOf(fp(1), fp(2), fp(3), fp(4)))
	if tip := v.Hits[0].Tooltip; tip.X <= 0 {
		t.Errorf("left half tooltip X = %v, want to the right of the day", tip.X)
	}
	if tip := v.Hits[3].Tooltip; tip.X+tip.Width >= 0 {
		t.Errorf("right half tooltip ends at %v, want left of the day", tip.X+tip.Width)
	}
}
