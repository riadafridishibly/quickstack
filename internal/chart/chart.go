// Package chart draws line and column charts as SVG on the server, with a
// tooltip per day that CSS shows on hover, and a table of the same data.
package chart

import (
	"fmt"
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/riadafridishibly/quickstack/internal/web"
)

// Charts are laid out without a viewBox: x positions are percentages and y
// positions are pixels, so the plot fills its card and text keeps its size
// at any width.
const (
	plotTop  = 8.0
	plotBase = 188.0
	// A column's rounded end is drawn past the baseline and clipped there,
	// which squares the end on the axis.
	barOverhang = 4.0
	// fixedBarDays is the most days that get fixed-width columns. Beyond it
	// columns are a share of the band so they fit a narrow card.
	fixedBarDays   = 7
	fixedBarWidth  = 20
	barBandShare   = 0.7
	maxDotDays     = 7
	maxAxisLabels  = 4
	tooltipCharPx  = 7.0
	tooltipRowPx   = 18.0
	tooltipOffset  = 10.0
	tooltipKeyPx   = 22.0
	tooltipPadding = 10.0
)

type Kind int

const (
	Columns Kind = iota
	Lines
)

// Series is one line or set of columns. Stroke and Fill are Tailwind classes
// written out in full so the stylesheet build finds them. A nil value is a
// day with nothing to plot. FullName is set when Name is shortened.
// Placeholder marks a Name the dashboard made up. Back draws the Series
// behind the others.
type Series struct {
	Name        string
	FullName    string
	Placeholder bool
	Back        bool
	Stroke      string
	Fill        string
	Values      []*float64
}

var (
	Slot1 = Series{Stroke: "stroke-chart-1", Fill: "fill-chart-1"}
	Slot2 = Series{Stroke: "stroke-chart-2", Fill: "fill-chart-2"}
	// Slots are in the order the palette was validated in, so adjacent Series
	// stay distinct with color vision deficiency.
	Slots = []Series{
		Slot1, Slot2,
		{Stroke: "stroke-chart-3", Fill: "fill-chart-3"},
		{Stroke: "stroke-chart-4", Fill: "fill-chart-4"},
		{Stroke: "stroke-chart-5", Fill: "fill-chart-5"},
	}
	// SlotOther is a gray for a Series that pools the rest.
	SlotOther = Series{Stroke: "stroke-chart-other", Fill: "fill-chart-other", Back: true}
)

// ValueFormat says how a chart's values read on its axis and in its tooltip.
type ValueFormat struct {
	Value func(float64) string
	Tick  func(float64) string
	Ticks func(max float64) []float64
}

var (
	CountFormat = ValueFormat{
		Value: func(v float64) string { return web.Count(int64(math.Round(v))) },
		Tick:  compactCount,
		Ticks: func(max float64) []float64 { return niceTicks(max, 1) },
	}
	MillisFormat = ValueFormat{
		Value: formatMillis,
		Tick:  formatMillis,
		Ticks: func(max float64) []float64 { return niceTicks(max, 1) },
	}
	NumberFormat = ValueFormat{
		Value: formatNumber,
		Tick:  formatNumber,
		Ticks: numberTicks,
	}
	DurationFormat = ValueFormat{
		Value: formatDuration,
		Tick:  formatDuration,
		Ticks: durationTicks,
	}
)

// Data is what a page knows about a chart before layout.
type Data struct {
	Title  string
	Note   string
	Empty  string
	Kind   Kind
	Days   []time.Time
	Series []Series
	Format ValueFormat
	// PlotZeros draws a chart whose values are all zero. Otherwise such a
	// chart has nothing to show.
	PlotZeros bool
	// Legend shows the legend for a single Series, whose name the title
	// doesn't give.
	Legend bool
}

type chartTick struct {
	Y     float64
	Label string
}

type axisLabel struct {
	X      string
	Anchor string
	Text   string
}

type chartBar struct {
	Center string
	Y      float64
	Height float64
}

type chartSegment struct {
	X1, X2 string
	Y1, Y2 float64
}

type chartDot struct {
	X string
	Y float64
}

type chartLine struct {
	Series   Series
	Segments []chartSegment
	Dots     []chartDot
}

type tooltipRow struct {
	Stroke      string
	Value       string
	Name        string
	Placeholder bool
	Y           float64
}

type chartHit struct {
	X, Width, Center string
	Tooltip          tooltip
}

type tooltip struct {
	Date   string
	X      float64
	Width  float64
	Height float64
	Rows   []tooltipRow
}

type tableRow struct {
	Date   string
	Values []string
}

// chartView is a chart laid out for rendering.
type chartView struct {
	Data
	HasData   bool
	Ticks     []chartTick
	Labels    []axisLabel
	Bars      []chartBar
	BarX      string
	BarWidth  string
	BarRadius string
	Lines     []chartLine
	Hits      []chartHit
	Rows      []tableRow
}

// layout computes the positions of everything chartView draws.
func layout(c Data) chartView {
	v := chartView{Data: c}
	top, bottom, present := 0.0, 0.0, false
	for _, s := range c.Series {
		for _, p := range s.Values {
			if p != nil {
				top, bottom, present = max(top, *p), min(bottom, *p), true
			}
		}
	}
	v.HasData = top > 0 || bottom < 0 || (c.PlotZeros && present)
	v.Rows = tableRows(c)
	if !v.HasData {
		return v
	}

	ticks := axisTicks(c.Format, bottom, top)
	low, high := ticks[0], ticks[len(ticks)-1]
	// Halved so the span of the largest values ingest accepts stays finite.
	y := func(val float64) float64 { return plotBase - (val/2-low/2)/(high/2-low/2)*(plotBase-plotTop) }
	for _, t := range ticks {
		v.Ticks = append(v.Ticks, chartTick{Y: y(t), Label: c.Format.Tick(t)})
	}
	v.Labels = axisLabels(c.Days)

	n := len(c.Days)
	band := 100 / float64(n)
	center := func(i int) float64 { return (float64(i) + 0.5) * band }

	switch c.Kind {
	case Columns:
		v.BarX, v.BarWidth, v.BarRadius = barGeometry(n, band)
		for i, p := range c.Series[0].Values {
			if p == nil || *p <= 0 {
				continue
			}
			// A small nonzero count stays visible.
			h := max(plotBase-y(*p), 2)
			v.Bars = append(v.Bars, chartBar{Center: pct(center(i)), Y: plotBase - h, Height: h + barOverhang})
		}
	case Lines:
		for _, back := range []bool{true, false} {
			for _, s := range c.Series {
				if s.Back == back {
					v.Lines = append(v.Lines, lineFor(s, n, center, y))
				}
			}
		}
	}

	for i, d := range c.Days {
		v.Hits = append(v.Hits, chartHit{
			X:       pct(float64(i) * band),
			Width:   pct(band),
			Center:  pct(center(i)),
			Tooltip: tooltipFor(c, i, d, center(i)),
		})
	}
	return v
}

func barGeometry(n int, band float64) (x, width, radius string) {
	if n <= fixedBarDays {
		return px(-fixedBarWidth / 2.0), px(fixedBarWidth), "4"
	}
	w := band * barBandShare
	return pct(-w / 2), pct(w), "2"
}

func lineFor(s Series, n int, center func(int) float64, y func(float64) float64) chartLine {
	l := chartLine{Series: s}
	last := -1
	for i := n - 1; i >= 0; i-- {
		if s.Values[i] != nil {
			last = i
			break
		}
	}
	has := func(i int) bool { return i >= 0 && i < n && s.Values[i] != nil }
	for i, p := range s.Values {
		if p == nil {
			continue
		}
		if has(i + 1) {
			l.Segments = append(l.Segments, chartSegment{
				X1: pct(center(i)), Y1: y(*p),
				X2: pct(center(i + 1)), Y2: y(*s.Values[i+1]),
			})
		}
		// A point with no neighbors has no segment to show it.
		if n <= maxDotDays || i == last || (!has(i-1) && !has(i+1)) {
			l.Dots = append(l.Dots, chartDot{X: pct(center(i)), Y: y(*p)})
		}
	}
	return l
}

func tooltipFor(c Data, i int, day time.Time, center float64) tooltip {
	t := tooltip{Date: day.Format("Mon, Jan 2")}
	chars := utf8.RuneCountInString(t.Date)
	textChars := 0
	for r, s := range c.Series {
		row := tooltipRow{
			Stroke: s.Stroke, Name: s.Name, Placeholder: s.Placeholder, Value: "No data",
			Y: plotTop + 22 + float64(r+1)*tooltipRowPx,
		}
		if p := s.Values[i]; p != nil {
			row.Value = c.Format.Value(*p)
		}
		textChars = max(textChars, utf8.RuneCountInString(row.Value)+1+utf8.RuneCountInString(row.Name))
		t.Rows = append(t.Rows, row)
	}
	t.Width = 2*tooltipPadding + max(float64(chars)*tooltipCharPx, tooltipKeyPx+float64(textChars)*tooltipCharPx)
	t.Height = 30 + float64(len(c.Series))*tooltipRowPx
	t.X = tooltipOffset
	if center > 50 {
		t.X = -tooltipOffset - t.Width
	}
	return t
}

func tableRows(c Data) []tableRow {
	rows := make([]tableRow, len(c.Days))
	for i, d := range c.Days {
		rows[i].Date = d.Format("2006-01-02")
		for _, s := range c.Series {
			text := "–"
			if p := s.Values[i]; p != nil {
				text = c.Format.Value(*p)
			}
			rows[i].Values = append(rows[i].Values, text)
		}
	}
	return rows
}

// axisLabels spreads at most maxAxisLabels dates across the axis, always
// showing the first and the last.
func axisLabels(days []time.Time) []axisLabel {
	n := len(days)
	if n == 0 {
		return nil
	}
	band := 100 / float64(n)
	step := max(1, (n+maxAxisLabels-3)/(maxAxisLabels-1))
	var labels []axisLabel
	for i := 0; i < n; i += step {
		if i != n-1 && n-1-i < step/2 {
			continue
		}
		labels = append(labels, axisLabelAt(days[i], (float64(i)+0.5)*band))
	}
	if (n-1)%step != 0 {
		labels = append(labels, axisLabelAt(days[n-1], (float64(n-1)+0.5)*band))
	}
	return labels
}

func axisLabelAt(day time.Time, center float64) axisLabel {
	l := axisLabel{X: pct(center), Anchor: "middle", Text: day.Format("Jan 2")}
	switch {
	case center < 12:
		l.X, l.Anchor = "0%", "start"
	case center > 88:
		l.X, l.Anchor = "100%", "end"
	}
	return l
}

// niceTicks returns evenly spaced ticks from 0 to at least top, on a 1, 2 or
// 5 step no smaller than minStep.
func niceTicks(top, minStep float64) []float64 {
	if top <= 0 {
		return []float64{0, minStep}
	}
	raw := top / 4
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	step := 10 * mag
	for _, m := range []float64{1, 2, 5} {
		if m*mag >= raw {
			step = m * mag
			break
		}
	}
	step = max(step, minStep)
	ticks := make([]float64, int(math.Ceil(top/step-1e-9))+1)
	for i := range ticks {
		ticks[i] = float64(i) * step
	}
	return ticks
}

// numberTicks allows any step, since the values can be arbitrarily small.
func numberTicks(top float64) []float64 {
	if top <= 0 {
		return []float64{0, 1}
	}
	return niceTicks(top, 0)
}

// axisTicks extends a format's ticks below zero when the values go there.
func axisTicks(f ValueFormat, bottom, top float64) []float64 {
	if bottom >= 0 {
		return f.Ticks(top)
	}
	span := top - bottom
	if math.IsInf(span, 0) {
		return []float64{bottom, 0, top}
	}
	base := f.Ticks(span)
	step := base[1] - base[0]
	var ticks []float64
	for k := math.Floor(bottom / step); ; k++ {
		ticks = append(ticks, k*step)
		if k*step >= top {
			return ticks
		}
	}
}

// durationTicks picks whole seconds, minutes or hours.
func durationTicks(top float64) []float64 {
	unit := 1.0
	switch {
	case top >= 7200:
		unit = 3600
	case top >= 120:
		unit = 60
	}
	ticks := niceTicks(top/unit, 1)
	for i := range ticks {
		ticks[i] *= unit
	}
	return ticks
}

func formatMillis(v float64) string {
	switch {
	case v <= 0:
		return "0"
	case v < 999.5:
		return fmt.Sprintf("%.0f ms", v)
	}
	return strconv.FormatFloat(v/1000, 'f', 1, 64) + " s"
}

// formatNumber is for values whose unit only the client knows, and which can
// be any size ingest accepts.
func formatNumber(v float64) string {
	if abs := math.Abs(v); abs >= 1000 && abs < 1e15 {
		return web.Count(int64(math.Round(v)))
	}
	return strconv.FormatFloat(v, 'g', 4, 64)
}

func formatDuration(seconds float64) string {
	total := int64(math.Round(seconds))
	switch {
	case total <= 0:
		return "0"
	case total < 60:
		return fmt.Sprintf("%ds", total)
	case total < 3600:
		if s := total % 60; s != 0 {
			return fmt.Sprintf("%dm %ds", total/60, s)
		}
		return fmt.Sprintf("%dm", total/60)
	}
	if m := total % 3600 / 60; m != 0 {
		return fmt.Sprintf("%dh %dm", total/3600, m)
	}
	return fmt.Sprintf("%dh", total/3600)
}

func compactCount(v float64) string {
	switch {
	case v >= 1_000_000:
		return strconv.FormatFloat(v/1_000_000, 'f', -1, 64) + "M"
	case v >= 10_000:
		return strconv.FormatFloat(v/1_000, 'f', -1, 64) + "K"
	}
	return web.Count(int64(v))
}

func pct(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) + "%" }

func px(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// WithValues is s, a slot such as Slot1, with a name and values.
func WithValues(s Series, name string, values []*float64) Series {
	s.Name, s.Values = name, values
	return s
}

// Value is n as a point to plot.
func Value(n int64) *float64 {
	v := float64(n)
	return &v
}
