// Package draw builds chart URLs — the charts in a report are made of the
// same numbers as the tables.
//
// **The data lives in the URL.** x-cmd's draw service renders an SVG from the
// query string, so a chart is a link: it can be pasted into an issue, a
// release note or a repository, and it renders there without anything
// installed. The SVG carries the data in a `<metadata>` element, so the
// numbers can be read back out of the picture.
//
// Two forms of every chart: the picture (`/draw`) and the explorer
// (`/draw/explore`), which is the same URL and lets a reader hover the
// numbers, change the parameters and take the data away.
package draw

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// The service that renders the charts.
const service = "https://x-cmd.com/draw"

// One series: a name and its numbers.
type Series struct {
	// The name of the series — the legend.
	Name string
	// The numbers, in the order of the labels.
	Values []float64
}

// URL: the chart's URL, and the explorer's, for the given type.
//
// The style is the x-cmd one (`t=evil`): the charts in a report should look
// like the project's other pictures.
//
// **The combo is the exception.** With `t=evil` the combo drops every bar
// series but the first and never draws the second y-axis — so the line's
// numbers end up on the bars' scale, which is worse than no chart at all
// (reported: https://github.com/x-cmd/draw/issues/6). The combo therefore
// keeps the default style, where both work.
func URL(chartType string, title string, unit string, labels []string, series []Series) (string, string) {
	return build(chartType, title, unit, labels, series, "", nil)
}

// Combo: a mixed chart — bars and a line, on two axes.
//
// The bars are the quantities; the line is the one that lives on another
// scale (a latency next to a size, a ratio next to a count). When the scales
// differ enough, the line gets its own axis — otherwise its numbers would be
// read on the bars' scale, which is a lie.
func Combo(title string, unit string, labels []string, bars []Series, line Series) (string, string) {
	return build("combo", title, unit, labels, bars, line.Name, line.Values)
}

// build: the URL for a chart.
//
// The style is empty for the combo (see [`URL`]).
func build(
	chartType string,
	title string,
	unit string,
	labels []string,
	series []Series,
	lineName string,
	lineValues []float64,
) (string, string) {
	values := url.Values{}
	values.Set("type", chartType)
	if chartType != "combo" {
		values.Set("t", "evil")
	}
	if title != "" {
		values.Set("title", title)
	}
	if unit != "" {
		values.Set("unit", unit)
	}
	if len(labels) > 0 {
		values.Set("labels", strings.Join(labels, ","))
	}
	for _, one := range series {
		values.Add("series", one.Name+":"+strings.Join(numbers(one.Values), ","))
	}
	if lineName != "" {
		values.Set("line", lineName+":"+strings.Join(numbers(lineValues), ","))
	}
	// The canvas: wide enough for the legend, tall enough for the plot.
	values.Set("w", "960")
	values.Set("h", "520")
	query := values.Encode()
	return service + "?" + query, service + "/explore?" + query
}

// numbers: a series' values, as text.
func numbers(values []float64) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, format(value))
	}
	return out
}

// Line: a line chart — a trend over the labels.
func Line(title string, unit string, labels []string, series []Series) (string, string) {
	return URL("line", title, unit, labels, series)
}

// Bar: a bar chart — a comparison across the labels.
func Bar(title string, unit string, labels []string, series []Series) (string, string) {
	return URL("bar", title, unit, labels, series)
}

// format: a number, without the noise.
func format(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// Markdown: the picture, and the link to the explorer.
//
// The form a reader wants in an issue: the chart, and clicking it opens the
// explorer, where the numbers can be hovered and taken away.
func Markdown(picture string, explorer string) string {
	return fmt.Sprintf("[![%s](%s)](%s)", "chart", picture, explorer)
}
