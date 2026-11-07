package report

import (
	"strings"
	"testing"

	"github.com/sumimi-dev/sumimi-gotest/internal/machine"
)

// The report has to carry the machine: a number without the machine it
// belongs to is not a measurement.
func TestTheReportCarriesTheMachine(t *testing.T) {
	document := New("sumimi-gotest", "0.1.0")
	document.Server = &machine.Facts{
		CPU:      "Intel Xeon 6975P-C",
		Cores:    2,
		RAMBytes: 7_600_000_000,
		OS:       "linux",
	}
	document.ServerVersion = "0.4.0"
	document.Points = []Point{
		{Rate: 5000, Offered: 100_000, Accepted: 100_000, Achieved: 5000, PeakRSSBytes: 84 << 20},
		{Rate: 20_000, Offered: 400_000, Accepted: 390_000, Refused: 10_000, Achieved: 19_998, PeakRSSBytes: 253 << 20},
	}

	markdown := document.Markdown()
	for _, wanted := range []string{
		"Intel Xeon 6975P-C",
		"2 cores",
		"linux",
		"5 000",
		"20 000",
		"84 MB",
		"253 MB",
		"Sumimi 0.4.0",
	} {
		if !strings.Contains(markdown, wanted) {
			t.Fatalf("the report does not mention %q:\n%s", wanted, markdown)
		}
	}
}

// A server that does not say what machine it is must not be papered over.
func TestAnUnknownServerIsSaidToBeUnknown(t *testing.T) {
	document := New("sumimi-gotest", "0.1.0")
	markdown := document.Markdown()
	if !strings.Contains(markdown, "unknown") {
		t.Fatalf("the report does not say that the machine is unknown:\n%s", markdown)
	}
}
