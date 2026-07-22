package dashboard

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

	"agent-desk/internal/pkg/dto/response"
)

func TestBillingCSVCellPreventsFormulaInjection(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"=1+1", "+cmd", "-2+3", "@SUM(A1:A2)", "\t=HYPERLINK(\"https://example.com\")"} {
		value := billingCSVCell(input)
		if !strings.HasPrefix(value, "'") {
			t.Fatalf("billingCSVCell(%q)=%q, want apostrophe prefix", input, value)
		}
	}
	if value := billingCSVCell("normal value"); value != "normal value" {
		t.Fatalf("normal CSV value changed: %q", value)
	}
}

func TestBillingCSVWritersEscapeUntrustedNamesAndRequestIDs(t *testing.T) {
	t.Parallel()
	data := &response.BillingQueryResponse{
		Official: response.BillingOfficialSectionResponse{Stores: []response.BillingOfficialStoreResponse{{
			TenantName: "=tenant", StoreID: 9, StoreName: "+store", Status: "ready",
			ModelNames: []string{"@model"}, Logs: []response.BillingOfficialUsageLogResponse{{
				StoreID: 9, StoreName: "+store", ModelName: "-model", RequestID: "=request",
			}},
		}}},
	}

	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	writeBillingOfficialCSV(writer, data)
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(output.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("CSV record count=%d, want 3", len(records))
	}
	for rowIndex, columnIndexes := range map[int][]int{0: {1, 2, 4}, 1: {1, 2, 4}, 2: {1, 2, 3, 4}} {
		for _, columnIndex := range columnIndexes {
			if !strings.HasPrefix(records[rowIndex][columnIndex], "'") {
				t.Fatalf("CSV cell [%d,%d]=%q is not escaped", rowIndex, columnIndex, records[rowIndex][columnIndex])
			}
		}
	}
}
