package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	defaultIntegrationBalanceDate       = "2026-01-31"
	defaultIntegrationAnalysisStartDate = "2026-01-01"
	defaultIntegrationAnalysisEndDate   = "2026-01-31"
	defaultIntegrationBenchmarkID       = "000300.XSHG"
	defaultIntegrationBaseURL           = "https://anka.ricequant.com:8085/"
	defaultIntegrationUsername          = "demo@ricequant.com"
	defaultIntegrationPassword          = "123456"
	defaultIntegrationWorkspace         = "灵均自动化测试"
	defaultIntegrationProfile           = "rqams-cli-integration-test"
	integrationMutationProductPrefix    = "rqamsc_it_"
	integrationMutationStartDate        = "2026-01-01"
	integrationMutationTradeDate        = "2026-01-05"
	integrationMutationCustodianDate    = "2026-01-06"
	integrationMutationUnitDate         = "2026-01-07"
	integrationMutationValuationDate    = "2026-01-08"
)

type cliEnvelope struct {
	OK       bool           `json:"ok"`
	Command  string         `json:"command"`
	Data     map[string]any `json:"data"`
	Metadata map[string]any `json:"metadata"`
	Error    map[string]any `json:"error"`
}

func TestIntegrationWorkspaceProductAndProductGroupAPIs(t *testing.T) {
	setupIntegrationSession(t)

	currentWorkspace := runIntegrationCommand(t, []string{"get", "current-workspace", "--payload", "{}"})
	if strings.TrimSpace(stringValue(currentWorkspace.Data["workspace_id"])) == "" {
		t.Fatalf("current workspace did not return workspace_id: %#v", currentWorkspace.Data)
	}

	testIntegrationProductAPIs(t)
	testIntegrationProductGroupAPIs(t)
	testIntegrationAnalysisAPIs(t)
}

func TestIntegrationBalanceDocumentFields(t *testing.T) {
	setupIntegrationSession(t)

	balanceDate := defaultIntegrationBalanceDate
	fullFields := []string{
		"date", "market_value", "total_assets", "total_liabilities", "total_equity", "daily_pnl",
		"daily_returns", "units", "unit_net_value", "acc_unit_net_value", "adjusted_net_value",
		"risk_exposure", "net_risk_exposure", "long_market_value", "short_market_value",
		"long_leverage", "long_net_risk_exposure", "positions",
	}
	productID, full := integrationBalanceWithPositions(t, balanceDate, fullFields)
	if productID == "" {
		t.Skip("balance field checks skipped because no product with positions was returned")
	}

	summaryPayload := map[string]any{
		"product_like_id_or_name": productID,
		"fields":                  []string{"total_equity", "unit_net_value", "daily_pnl"},
	}
	if balanceDate != "" {
		summaryPayload["date"] = balanceDate
	}
	summary := runIntegrationCommand(t, []string{"get", "balance", "--payload", integrationPayload(t, summaryPayload)})
	assertIntegrationFields(t, "get balance summary fields", summary.Data, []string{"total_equity", "unit_net_value", "daily_pnl"})
	if _, ok := summary.Data["positions"]; ok {
		t.Fatalf("get balance with summary fields should not return positions: %#v", summary.Data)
	}

	assertIntegrationFields(t, "get balance documented top-level fields", full.Data, []string{
		"date", "total_equity", "daily_pnl", "daily_returns", "risk_exposure", "net_risk_exposure", "positions",
	})
	assertIntegrationPositionFields(t, full.Data["positions"])

	snapshot := runIntegrationCommand(
		t,
		[]string{"get", "asset-snapshot", "--payload", integrationPayload(t, map[string]any{
			"product_like_id_or_name": productID,
			"fields":                  []string{"risk_exposure", "net_risk_exposure", "excess_returns"},
		})},
	)
	if len(snapshot.Data) == 0 {
		t.Log("asset-snapshot returned an empty object; realtime field checks skipped")
	} else {
		assertIntegrationFields(t, "asset-snapshot documented fields", snapshot.Data, []string{
			"risk_exposure", "net_risk_exposure", "excess_returns",
		})
	}

	rawSeries := strings.TrimSpace(runRawIntegrationCommand(
		t,
		[]string{"get", "balance-series", "--payload", integrationPayload(t, map[string]any{
			"product_like_id_or_name": productID,
			"start_date":              balanceDate,
			"end_date":                balanceDate,
			"fields":                  []string{"avg_price", "fair_value", "acc_pnl"},
			"format":                  "ndjson",
		})},
	))
	if rawSeries == "" {
		t.Logf("balance-series returned no rows for %s; series field checks skipped", balanceDate)
		return
	}
	var firstSeriesRow map[string]any
	if err := json.Unmarshal([]byte(strings.Split(rawSeries, "\n")[0]), &firstSeriesRow); err != nil {
		t.Fatalf("balance-series NDJSON row should be JSON object: %v\nrow: %s", err, rawSeries)
	}
	assertIntegrationFields(t, "balance-series documented fields", firstSeriesRow, []string{"date", "total_equity", "daily_pnl", "daily_returns"})
}

func TestIntegrationValuationReportDocumentFields(t *testing.T) {
	setupIntegrationSession(t)

	productID, valuationReports := integrationValuationReportList(t)
	if len(valuationReports) == 0 {
		t.Skip("valuation report field checks skipped because no valuation reports were found")
	}
	first, ok := valuationReports[0].(map[string]any)
	if !ok {
		t.Fatalf("valuation_reports[0] should be an object: %#v", valuationReports[0])
	}
	assertIntegrationFields(t, "valuation report list documented fields", first, []string{
		"valuation_report_id", "date", "file_name", "source",
	})

	raw := strings.TrimSpace(runRawIntegrationCommand(
		t,
		[]string{"get", "valuation-report-list", "--payload", integrationPayload(t, map[string]any{
			"product_id_or_name": productID,
			"fields":             []string{"valuation_report_id", "date", "file_name", "source"},
			"limit":              1,
			"format":             "ndjson",
		})},
	))
	if raw == "" {
		t.Fatalf("valuation-report-list NDJSON should return one row for product %s", productID)
	}
	var ndjsonRow map[string]any
	if err := json.Unmarshal([]byte(strings.Split(raw, "\n")[0]), &ndjsonRow); err != nil {
		t.Fatalf("valuation-report-list NDJSON row should be JSON object: %v\nrow: %s", err, raw)
	}
	assertIntegrationFields(t, "valuation report NDJSON documented fields", ndjsonRow, []string{
		"valuation_report_id", "date", "file_name", "source",
	})
}

func TestIntegrationEventDocumentFields(t *testing.T) {
	setupIntegrationSession(t)

	t.Run("custodian events", func(t *testing.T) {
		productID, events := integrationFirstNonEmptyEventList(
			t,
			"get custodian-event-list",
			"custodian_events",
			map[string]any{
				"fields": []string{"id", "date", "custodian_event_type", "amount"},
				"limit":  1,
			},
		)
		if len(events) == 0 {
			t.Skip("custodian event field checks skipped because no custodian events were found")
		}
		first, ok := events[0].(map[string]any)
		if !ok {
			t.Fatalf("custodian_events[0] should be an object: %#v", events[0])
		}
		assertIntegrationFields(t, "custodian event documented fields", first, []string{
			"date", "custodian_event_type", "amount",
		})

		raw := strings.TrimSpace(runRawIntegrationCommand(
			t,
			[]string{"get", "custodian-event-list", "--payload", integrationPayload(t, map[string]any{
				"product_id_or_name": productID,
				"fields":             []string{"date", "custodian_event_type", "amount"},
				"limit":              1,
				"format":             "ndjson",
			})},
		))
		if raw == "" {
			t.Fatalf("custodian-event-list NDJSON should return one row for product %s", productID)
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(strings.Split(raw, "\n")[0]), &row); err != nil {
			t.Fatalf("custodian-event-list NDJSON row should be JSON object: %v\nrow: %s", err, raw)
		}
		assertIntegrationFields(t, "custodian event NDJSON documented fields", row, []string{
			"date", "custodian_event_type", "amount",
		})
	})

	t.Run("unit events", func(t *testing.T) {
		productID, events := integrationFirstNonEmptyEventList(
			t,
			"get unit-event-list",
			"daily_units",
			map[string]any{
				"include_auto_units": true,
				"fields":             []string{"date", "subscription_units", "redemption_units", "source"},
				"limit":              1,
			},
		)
		if len(events) == 0 {
			t.Skip("unit event field checks skipped because no unit events were found")
		}
		first, ok := events[0].(map[string]any)
		if !ok {
			t.Fatalf("daily_units[0] should be an object: %#v", events[0])
		}
		assertIntegrationFields(t, "unit event documented fields", first, []string{
			"date", "subscription_units", "redemption_units", "source",
		})

		unitList := runIntegrationCommand(t, []string{"get", "unit-event-list", "--payload", integrationPayload(t, map[string]any{
			"product_id_or_name": productID,
			"include_auto_units": true,
			"limit":              1,
		})})
		changes, ok := unitList.Data["unit_changes"].([]any)
		if !ok {
			t.Fatalf("unit-event-list should return data.unit_changes[]: %#v", unitList.Data)
		}
		if len(changes) > 0 {
			firstChange, ok := changes[0].(map[string]any)
			if !ok {
				t.Fatalf("unit_changes[0] should be an object: %#v", changes[0])
			}
			assertIntegrationFields(t, "unit_changes documented fields", firstChange, []string{
				"product_id", "date", "subscription_units", "redemption_units", "units",
			})
		}

		raw := strings.TrimSpace(runRawIntegrationCommand(
			t,
			[]string{"get", "unit-event-list", "--payload", integrationPayload(t, map[string]any{
				"product_id_or_name": productID,
				"include_auto_units": true,
				"fields":             []string{"date", "subscription_units", "redemption_units", "source"},
				"limit":              1,
				"format":             "ndjson",
			})},
		))
		if raw == "" {
			t.Fatalf("unit-event-list NDJSON should return one row for product %s", productID)
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(strings.Split(raw, "\n")[0]), &row); err != nil {
			t.Fatalf("unit-event-list NDJSON row should be JSON object: %v\nrow: %s", err, raw)
		}
		assertIntegrationFields(t, "unit event NDJSON documented fields", row, []string{
			"date", "subscription_units", "redemption_units", "source",
		})
	})
}

func TestIntegrationPaperTradingDocumentFields(t *testing.T) {
	setupIntegrationSession(t)

	listFields := []string{
		"product_id", "name", "status", "strategy_model", "benchmark", "start_date", "init_amount", "algo",
		"stock_min_fee", "stock_commission_rate", "loan_rate", "margin_rate", "strategy_category",
	}
	list := runIntegrationArrayCommand(
		t,
		[]string{"get", "paper-trading-list", "--payload", integrationPayload(t, map[string]any{
			"fields": listFields,
			"limit":  1,
		})},
	)
	if len(list.Data) == 0 {
		t.Skip("paper-trading field checks skipped because no paper trading configs were found")
	}
	first, ok := list.Data[0].(map[string]any)
	if !ok {
		t.Fatalf("paper-trading-list item should be object: %#v", list.Data[0])
	}
	assertIntegrationFields(t, "paper-trading-list documented fields", first, []string{"product_id", "status", "strategy_model"})
	assertIntegrationNoFields(t, "paper-trading-list internal fields", first, []string{"_id", "version"})

	raw := strings.TrimSpace(runRawIntegrationCommand(
		t,
		[]string{"get", "paper-trading-list", "--payload", integrationPayload(t, map[string]any{
			"fields": []string{"product_id", "strategy_model"},
			"limit":  1,
			"format": "ndjson",
		})},
	))
	if raw == "" {
		t.Fatalf("paper-trading-list NDJSON should return one row when configs exist")
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(strings.Split(raw, "\n")[0]), &row); err != nil {
		t.Fatalf("paper-trading-list NDJSON row should be JSON object: %v\nrow: %s", err, raw)
	}
	assertIntegrationFields(t, "paper-trading-list NDJSON documented fields", row, []string{"product_id", "strategy_model"})
	assertIntegrationNoFields(t, "paper-trading-list NDJSON internal fields", row, []string{"_id", "version"})

	productID := stringValue(first["product_id"])
	if productID == "" {
		t.Fatalf("paper-trading-list item should include product_id: %#v", first)
	}
	detail := runIntegrationCommand(t, []string{"get", "paper-trading", "--payload", integrationPayload(t, map[string]any{
		"product_id_or_name": productID,
	})})
	assertIntegrationFields(t, "paper-trading detail documented fields", detail.Data, []string{"product_id", "status", "strategy_model"})
	assertIntegrationNoFields(t, "paper-trading detail internal fields", detail.Data, []string{"_id", "version"})

	signals := runIntegrationAnyCommand(t, []string{"get", "paper-trading-signal-list", "--payload", integrationPayload(t, map[string]any{
		"product_id_or_name": productID,
		"fields":             []string{"id", "date", "status", "type"},
		"limit":              1,
	})})
	signalItems := integrationListData(t, "paper-trading-signal-list", signals.Data, "signals")
	if len(signalItems) == 0 {
		t.Logf("paper-trading signal field checks skipped because product %s has no signals", productID)
		return
	}
	firstSignal, ok := signalItems[0].(map[string]any)
	if !ok {
		t.Fatalf("paper-trading-signal-list item should be object: %#v", signalItems[0])
	}
	assertIntegrationFields(t, "paper-trading-signal-list documented fields", firstSignal, []string{"id"})
	assertIntegrationNoFields(t, "paper-trading-signal-list internal fields", firstSignal, []string{"_id", "signal_id", "version"})

	signalID := stringValue(firstSignal["id"])
	if signalID == "" {
		t.Fatalf("paper-trading-signal-list item should include id: %#v", firstSignal)
	}
	signal := runIntegrationCommand(t, []string{"get", "paper-trading-signal", "--payload", integrationPayload(t, map[string]any{
		"product_id_or_name": productID,
		"signal_id":          signalID,
		"fields":             []string{"id", "date", "status", "type", "orders", "matching_results"},
	})})
	assertIntegrationFields(t, "paper-trading-signal documented fields", signal.Data, []string{"id"})
	assertIntegrationNoFields(t, "paper-trading-signal internal fields", signal.Data, []string{"_id", "signal_id", "version"})
}

func TestIntegrationMutablePaperTradingLifecycle(t *testing.T) {
	setupIntegrationSession(t)
	cleanupStaleIntegrationMutationProducts(t)

	now := time.Now().UTC()
	suffix := fmt.Sprintf("%s_%d", now.Format("20060102_150405"), now.UnixNano())
	testIntegrationMutablePaperTrading(t, suffix)
}

func TestIntegrationWorkspacePermissionAndCustomizedReadOnlyAPIs(t *testing.T) {
	setupIntegrationSession(t)

	workspaces := runIntegrationAnyCommand(t, []string{"get", "workspace-list", "--payload", "{}"})
	workspaceItems := integrationListData(t, "workspace-list", workspaces.Data, "data")
	if len(workspaceItems) == 0 {
		t.Fatalf("workspace-list should return at least one workspace: %#v", workspaces.Data)
	}

	productID := integrationProductID(t)
	if productID == "" {
		t.Skip("permission and customized indicator checks skipped because no product was returned")
	}
	permissions := runIntegrationCommand(t, []string{"get", "permission-list", "--payload", integrationPayload(t, map[string]any{
		"resource_type": "products",
		"resource_id":   productID,
		"fields":        []string{"id", "user_id", "permission"},
		"limit":         5,
	})})
	if _, ok := permissions.Data["permissions"].([]any); !ok {
		t.Fatalf("permission-list should return data.permissions[]: %#v", permissions.Data)
	}

	customizedIndicators := runIntegrationCommand(t, []string{"get", "customized-indicator", "--payload", integrationPayload(t, map[string]any{
		"product_like_id_or_name": productID,
	})})
	if customizedIndicators.Data == nil {
		t.Fatalf("customized-indicator should return an object, got nil")
	}

	benchmarks := runIntegrationAnyCommand(t, []string{"get", "customized-benchmark-list", "--payload", integrationPayload(t, map[string]any{
		"fields": []string{"id", "name"},
		"limit":  1,
	})})
	benchmarkItems := integrationListData(t, "customized-benchmark-list", benchmarks.Data, "customized_benchmarks")
	if len(benchmarkItems) > 0 {
		first, ok := benchmarkItems[0].(map[string]any)
		if !ok {
			t.Fatalf("customized-benchmark-list item should be object: %#v", benchmarkItems[0])
		}
		benchmarkID := firstStringFromMap(first, "id", "_id", "customized_benchmark_id")
		if benchmarkID != "" {
			benchmark := runIntegrationCommand(t, []string{"get", "customized-benchmark", "--payload", integrationPayload(t, map[string]any{
				"customized_benchmark_id": benchmarkID,
			})})
			if len(benchmark.Data) == 0 {
				t.Fatalf("customized-benchmark detail should return data: %#v", benchmark.Data)
			}
		}
	} else {
		t.Log("customized-benchmark detail check skipped because list is empty")
	}

	instruments := runIntegrationAnyCommand(t, []string{"get", "customized-instrument-list", "--payload", integrationPayload(t, map[string]any{
		"fields": []string{"id", "order_book_id", "symbol"},
		"limit":  1,
	})})
	instrumentItems := integrationListData(t, "customized-instrument-list", instruments.Data, "customized_instruments")
	if len(instrumentItems) > 0 {
		first, ok := instrumentItems[0].(map[string]any)
		if !ok {
			t.Fatalf("customized-instrument-list item should be object: %#v", instrumentItems[0])
		}
		instrumentID := firstStringFromMap(first, "id", "_id", "customized_ins_id")
		if instrumentID != "" {
			prices := runIntegrationAnyCommand(t, []string{"get", "customized-instrument-price", "--payload", integrationPayload(t, map[string]any{
				"customized_ins_id": instrumentID,
				"raw":               true,
				"limit":             5,
			})})
			if prices.Data == nil {
				t.Fatalf("customized-instrument-price should return data, got nil")
			}
		}
	} else {
		t.Log("customized-instrument-price check skipped because list is empty")
	}
}

func TestIntegrationAdditionalAnalysisReadOnlyAPIs(t *testing.T) {
	setupIntegrationSession(t)

	productID := integrationProductID(t)
	if productID == "" {
		t.Skip("additional analysis checks skipped because no product was returned")
	}
	startDate := defaultIntegrationAnalysisStartDate
	endDate := defaultIntegrationAnalysisEndDate
	benchmarkID := defaultIntegrationBenchmarkID
	basePayload := map[string]any{
		"product_like_ids_or_names": []string{productID},
		"start_date":                startDate,
		"end_date":                  endDate,
	}

	summary, ok := runIntegrationOptionalAnyCommand(t, []string{"get", "investment-overview-summary-indicator", "--payload", integrationPayload(t, cloneTestMap(basePayload))})
	if ok && summary.Data == nil {
		t.Fatalf("investment-overview-summary-indicator should return data, got nil")
	}
	assetCapitalSize, ok := runIntegrationOptionalAnyCommand(t, []string{"get", "investment-overview-asset-capital-size", "--payload", integrationPayload(t, cloneTestMap(basePayload))})
	if ok && assetCapitalSize.Data == nil {
		t.Fatalf("investment-overview-asset-capital-size should return data, got nil")
	}
	assetAllocation, ok := runIntegrationOptionalAnyCommand(t, []string{"get", "investment-overview-asset-allocation", "--payload", integrationPayload(t, cloneTestMap(basePayload))})
	if ok && assetAllocation.Data == nil {
		t.Fatalf("investment-overview-asset-allocation should return data, got nil")
	}
	returnsCorrelation, ok := runIntegrationOptionalAnyCommand(t, []string{"get", "investment-overview-returns-correlation", "--payload", integrationPayload(t, cloneTestMap(basePayload))})
	if ok && returnsCorrelation.Data == nil {
		t.Fatalf("investment-overview-returns-correlation should return data, got nil")
	}
	excessPayload := cloneTestMap(basePayload)
	excessPayload["benchmark_id"] = benchmarkID
	excessCorrelation, ok := runIntegrationOptionalAnyCommand(t, []string{"get", "investment-overview-excess-correlation", "--payload", integrationPayload(t, excessPayload)})
	if ok && excessCorrelation.Data == nil {
		t.Fatalf("investment-overview-excess-correlation should return data, got nil")
	}

	attributionPayload := map[string]any{
		"product_like_id_or_name": productID,
		"start_date":              startDate,
		"end_date":                endDate,
		"benchmark_id":            benchmarkID,
	}
	runIntegrationOptionalAnyCommand(t, []string{"get", "performance-attribution", "--payload", integrationPayload(t, cloneTestMap(attributionPayload))})
	runIntegrationOptionalAnyCommand(t, []string{"get", "returns-decomposition", "--payload", integrationPayload(t, attributionPayload)})
}

func TestIntegrationReconciliationAndReportReadOnlyAPIs(t *testing.T) {
	setupIntegrationSession(t)

	productID := integrationProductID(t)
	if productID == "" {
		t.Skip("reconciliation checks skipped because no product was returned")
	}
	date := defaultIntegrationBalanceDate
	reconciliationList := runIntegrationAnyCommand(t, []string{"get", "reconciliation-list", "--payload", integrationPayload(t, map[string]any{
		"product_ids": []string{productID},
		"start_date":  date,
		"end_date":    date,
		"limit":       1,
	})})
	if reconciliationList.Data == nil {
		t.Fatalf("reconciliation-list should return data, got nil")
	}

	productWithReportID, valuationReports := integrationValuationReportList(t)
	if len(valuationReports) > 0 {
		first, ok := valuationReports[0].(map[string]any)
		if !ok {
			t.Fatalf("valuation report item should be object: %#v", valuationReports[0])
		}
		reportDate := stringValue(first["date"])
		if reportDate != "" {
			reconciliationDiff, ok := runIntegrationOptionalCommand(t, []string{"get", "reconciliation-diff", "--payload", integrationPayload(t, map[string]any{
				"product_id": productWithReportID,
				"date":       reportDate,
				"fields":     []string{"positions", "prices", "payable", "receivable", "cash", "net_asset"},
			})})
			if ok && reconciliationDiff.Data == nil {
				t.Fatalf("reconciliation-diff should return data, got nil")
			}
		}
	} else {
		t.Log("reconciliation-diff check skipped because no valuation reports were found")
	}

	latest := runIntegrationAnyCommand(t, []string{"get", "position-statement-latest-list", "--payload", integrationPayload(t, map[string]any{"limit": 20})})
	latestItems := integrationListData(t, "position-statement-latest-list", latest.Data, "data")
	if len(latestItems) == 0 {
		t.Log("position-statement detail checks skipped because latest list is empty")
	} else if productID, assetUnitID, statementDate := firstPositionStatementLocator(latestItems); productID != "" && assetUnitID != "" {
		payload := map[string]any{
			"product_id":    productID,
			"asset_unit_id": assetUnitID,
			"start_date":    statementDate,
			"end_date":      statementDate,
			"limit":         5,
		}
		positionStatement := runIntegrationAnyCommand(t, []string{"get", "position-statement", "--payload", integrationPayload(t, payload)})
		if positionStatement.Data == nil {
			t.Fatalf("position-statement should return data, got nil")
		}
		if statementDate != "" {
			reconciliationAssetUnitDiff := runIntegrationAnyCommand(t, []string{"get", "reconciliation-asset-unit-diff", "--payload", integrationPayload(t, map[string]any{
				"product_id":    productID,
				"asset_unit_id": assetUnitID,
				"date":          statementDate,
			})})
			if reconciliationAssetUnitDiff.Data == nil {
				t.Fatalf("reconciliation-asset-unit-diff should return data, got nil")
			}
			reconciliationPositionStatement := runIntegrationAnyCommand(t, []string{"get", "reconciliation-position-statement", "--payload", integrationPayload(t, map[string]any{
				"product_id":    productID,
				"asset_unit_id": assetUnitID,
				"date":          statementDate,
			})})
			if reconciliationPositionStatement.Data == nil {
				t.Fatalf("reconciliation-position-statement should return data, got nil")
			}
		}
	} else {
		t.Log("position-statement detail checks skipped because latest list did not include product_id and asset_unit_id")
	}

	if len(valuationReports) > 0 {
		first, ok := valuationReports[0].(map[string]any)
		if !ok {
			t.Fatalf("valuation report item should be object: %#v", valuationReports[0])
		}
		reportID := firstStringFromMap(first, "valuation_report_id", "id", "_id")
		fileName := stringValue(first["file_name"])
		if reportID != "" {
			download := runIntegrationCommand(t, []string{"get", "valuation-report-file", "--payload", integrationPayload(t, map[string]any{
				"product_id":          productWithReportID,
				"valuation_report_id": reportID,
				"save_path":           t.TempDir(),
				"file_name":           fileName,
			})})
			if integrationDownloadPath(download.Data) == "" {
				t.Fatalf("valuation-report-file should return saved path: %#v", download.Data)
			}
		}
	} else {
		t.Log("valuation-report-file check skipped because no valuation reports were found")
	}

	weeklySavePath := t.TempDir()
	weekly, ok := runIntegrationOptionalCommand(t, []string{"get", "weekly-net-value-report", "--payload", integrationPayload(t, map[string]any{
		"product_like_id_or_name": productID,
		"start_date":              defaultIntegrationAnalysisStartDate,
		"end_date":                defaultIntegrationAnalysisEndDate,
		"save_path":               weeklySavePath,
		"file_name":               "weekly-net-value.xlsx",
	})})
	if ok {
		path := integrationDownloadPath(weekly.Data)
		if path == "" {
			t.Fatalf("weekly-net-value-report should return saved path: %#v", weekly.Data)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("weekly-net-value-report should save file under %s: %v", weeklySavePath, err)
		}
	}
}

func TestIntegrationMutableProductLifecycle(t *testing.T) {
	setupIntegrationSession(t)
	cleanupStaleIntegrationMutationProducts(t)

	now := time.Now().UTC()
	suffix := fmt.Sprintf("%s_%d", now.Format("20060102_150405"), now.UnixNano())
	productName := integrationMutationProductPrefix + suffix
	createPayload := integrationMutationProductPayload(productName)
	created := runIntegrationCommand(t, []string{"insert", "product", "--payload", integrationPayload(t, createPayload)})
	productID := firstStringFromMap(created.Data, "id", "_id", "product_id")
	if productID == "" {
		productID = integrationProductIDByName(t, productName)
	}
	if productID == "" {
		t.Fatalf("insert product did not return a product id and product-list could not find %q: %#v", productName, created.Data)
	}

	productDeleted := false
	t.Cleanup(func() {
		if !productDeleted {
			runIntegrationCleanupCommand(t, []string{"delete", "product", "--payload", integrationPayload(t, map[string]any{"product_id": productID})})
		}
	})

	product := runIntegrationCommand(t, []string{"get", "product", "--payload", integrationPayload(t, map[string]any{"product_id": productID})})
	if name := stringValue(product.Data["name"]); name != productName {
		t.Fatalf("created product name mismatch, want %q, got %q: %#v", productName, name, product.Data)
	}
	runIntegrationCommand(t, []string{"update", "product", "--payload", integrationPayload(t, map[string]any{
		"product_id":    productID,
		"update_fields": map[string]any{"description": "rqamsc mutable integration updated"},
	})})

	testIntegrationMutableTrade(t, productID, suffix)
	testIntegrationMutableSettlementTrade(t, productID)
	testIntegrationMutableCustodianEvent(t, productID)
	testIntegrationMutableUnitEvent(t, productID)
	testIntegrationMutableValuationReport(t, productID)

	runIntegrationCommand(t, []string{"delete", "product", "--payload", integrationPayload(t, map[string]any{"product_id": productID})})
	productDeleted = true
}

func setupIntegrationSession(t *testing.T) integrationEnv {
	t.Helper()
	cfg := requireIntegrationEnv(t)
	t.Setenv("RQAMS_CLI_CONFIG", filepath.Join(t.TempDir(), "config.json"))

	authPayload := `{"profile":` + quote(cfg.profile) + `,"base_url":` + quote(cfg.baseURL) + `,"username":` + quote(cfg.username) + `,"password":` + quote(cfg.password) + `}`
	auth := runIntegrationCommand(t, []string{"auth", "--payload", authPayload})
	if auth.Data["authenticated"] != true {
		t.Fatalf("auth did not report authenticated=true: %#v", auth.Data)
	}

	useWorkspace := runIntegrationCommand(
		t,
		[]string{"use", "workspace", "--payload", `{"profile":` + quote(cfg.profile) + `,"workspace_name_or_id":` + quote(cfg.workspace) + `}`},
	)
	if strings.TrimSpace(stringValue(useWorkspace.Data["workspace_id"])) == "" {
		t.Fatalf("use workspace did not return workspace_id: %#v", useWorkspace.Data)
	}
	return cfg
}

func testIntegrationProductAPIs(t *testing.T) {
	t.Helper()
	productList := runIntegrationCommand(
		t,
		[]string{"get", "product-list", "--payload", `{"fields":["id","name","start_date"],"limit":1}`},
	)
	products, ok := productList.Data["products"].([]any)
	if !ok {
		t.Fatalf("product-list should return data.products[]: %#v", productList.Data)
	}
	if _, ok := productList.Data["total"].(float64); !ok {
		t.Fatalf("product-list should return numeric data.total: %#v", productList.Data)
	}
	assertIntegrationNDJSON(t, []string{"get", "product-list", "--payload", `{"fields":["id","name","start_date"],"limit":1,"format":"ndjson"}`}, len(products))

	productID := ""
	if len(products) > 0 {
		first, ok := products[0].(map[string]any)
		if !ok {
			t.Fatalf("product-list item should be an object: %#v", products[0])
		}
		productID = stringValue(first["id"])
	}
	if productID == "" {
		t.Log("product detail check skipped because no product was returned")
		return
	}

	product := runIntegrationCommand(t, []string{"get", "product", "--payload", `{"product_id":` + quote(productID) + `}`})
	if stringValue(product.Data["id"]) != productID {
		t.Fatalf("get product should return matching data.id, want %s, got %#v", productID, product.Data)
	}
	if _, ok := product.Data["_id"]; ok {
		t.Fatalf("get product should expose id, not _id: %#v", product.Data)
	}
}

func testIntegrationProductGroupAPIs(t *testing.T) {
	t.Helper()
	productGroupList := runIntegrationCommand(
		t,
		[]string{"get", "product-group-list", "--payload", `{"fields":["id","name","start_date"],"limit":1}`},
	)
	productGroups, ok := productGroupList.Data["product_groups"].([]any)
	if !ok {
		t.Fatalf("product-group-list should return data.product_groups[]: %#v", productGroupList.Data)
	}
	if total, ok := productGroupList.Data["total"]; ok {
		if _, ok := total.(float64); !ok {
			t.Fatalf("product-group-list data.total should be numeric when returned: %#v", productGroupList.Data)
		}
	}
	assertIntegrationNDJSON(t, []string{"get", "product-group-list", "--payload", `{"fields":["id","name","start_date"],"limit":1,"format":"ndjson"}`}, len(productGroups))

	productGroupID := ""
	if len(productGroups) > 0 {
		first, ok := productGroups[0].(map[string]any)
		if !ok {
			t.Fatalf("product-group-list item should be an object: %#v", productGroups[0])
		}
		productGroupID = stringValue(first["id"])
	}
	if productGroupID == "" {
		t.Log("product group detail check skipped because no product group was returned")
		return
	}

	productGroup := runIntegrationCommand(
		t,
		[]string{"get", "product-group", "--payload", `{"product_group_id":` + quote(productGroupID) + `}`},
	)
	if stringValue(productGroup.Data["id"]) != productGroupID {
		t.Fatalf("get product-group should return matching data.id, want %s, got %#v", productGroupID, productGroup.Data)
	}
	if _, ok := productGroup.Data["_id"]; ok {
		t.Fatalf("get product-group should expose id, not _id: %#v", productGroup.Data)
	}
}

func testIntegrationAnalysisAPIs(t *testing.T) {
	t.Helper()
	productID := integrationProductID(t)
	if productID == "" {
		t.Log("analysis checks skipped because no product was returned")
		return
	}
	startDate := defaultIntegrationAnalysisStartDate
	endDate := defaultIntegrationAnalysisEndDate
	benchmarkID := defaultIntegrationBenchmarkID

	indicator := runIntegrationCommand(
		t,
		[]string{"get", "indicator", "--payload", integrationPayload(t, map[string]any{
			"product_like_id_or_name": productID,
			"start_date":              startDate,
			"end_date":                endDate,
		})},
	)
	for _, field := range []string{"daily_risk", "weekly_risk", "monthly_risk"} {
		if _, ok := indicator.Data[field].(map[string]any); !ok {
			t.Fatalf("get indicator should return data.%s object: %#v", field, indicator.Data)
		}
	}
	if _, ok := indicator.Data["date"]; ok {
		t.Fatalf("get indicator should not expose a top-level date field: %#v", indicator.Data)
	}

	series := runIntegrationCommand(
		t,
		[]string{"get", "indicator-series", "--payload", integrationPayload(t, map[string]any{
			"product_like_id_or_name": productID,
			"start_date":              startDate,
			"end_date":                endDate,
			"indicators":              []string{"total_equity", "daily_pnl"},
		})},
	)
	for _, field := range []string{"total_equity", "daily_pnl"} {
		if _, ok := series.Data[field].(map[string]any); !ok {
			t.Fatalf("get indicator-series should return data.%s date map: %#v", field, series.Data)
		}
	}

	overview := runIntegrationArrayCommand(
		t,
		[]string{"get", "investment-overview-returns-series", "--payload", integrationPayload(t, map[string]any{
			"product_like_ids_or_names": []string{productID},
			"start_date":                startDate,
			"end_date":                  endDate,
			"benchmark_id":              benchmarkID,
		})},
	)
	if len(overview.Data) == 0 {
		t.Fatalf("investment-overview-returns-series should return data[]")
	}
	first, ok := overview.Data[0].(map[string]any)
	if !ok {
		t.Fatalf("investment-overview-returns-series item should be object: %#v", overview.Data[0])
	}
	for _, field := range []string{"id", "name", "type", "daily", "weekly", "monthly"} {
		if _, ok := first[field]; !ok {
			t.Fatalf("investment-overview-returns-series item missing %s: %#v", field, first)
		}
	}
	for _, field := range []string{"task_id", "status", "progress"} {
		if _, ok := first[field]; ok {
			t.Fatalf("investment-overview-returns-series should expose business fields, not task field %s: %#v", field, first)
		}
	}

	tradingProductID, firstTrade, tradingDetail := integrationTradingAnalysisCase(t, startDate, endDate)
	if tradingProductID == "" {
		t.Log("trading-analysis field checks skipped because no candidate product returned a valid detail row")
		return
	}
	assertIntegrationFields(t, "trading-analysis-list documented fields", firstTrade, []string{
		"date", "asset_class", "asset_category", "order_book_id", "direction", "symbol", "period_pnl",
	})
	for _, field := range []string{"task_id", "status", "progress"} {
		if _, ok := firstTrade[field]; ok {
			t.Fatalf("trading-analysis-list should expose business fields, not task field %s: %#v", field, firstTrade)
		}
	}

	assertIntegrationFields(t, "trading-analysis documented fields", tradingDetail, []string{
		"prev_adjusted_price_series", "position_quantity_series", "pnl_series", "buy_points", "sell_points",
	})
}

func integrationProductID(t *testing.T) string {
	t.Helper()
	productList := runIntegrationCommand(
		t,
		[]string{"get", "product-list", "--payload", `{"fields":["id","name","start_date"],"limit":1}`},
	)
	products, ok := productList.Data["products"].([]any)
	if !ok || len(products) == 0 {
		return ""
	}
	first, ok := products[0].(map[string]any)
	if !ok {
		t.Fatalf("product-list item should be an object: %#v", products[0])
	}
	return stringValue(first["id"])
}

func integrationProductGroupID(t *testing.T) string {
	t.Helper()
	productGroupList := runIntegrationCommand(
		t,
		[]string{"get", "product-group-list", "--payload", `{"fields":["id","name","start_date"],"limit":1}`},
	)
	productGroups, ok := productGroupList.Data["product_groups"].([]any)
	if !ok || len(productGroups) == 0 {
		return ""
	}
	first, ok := productGroups[0].(map[string]any)
	if !ok {
		t.Fatalf("product-group-list item should be an object: %#v", productGroups[0])
	}
	return stringValue(first["id"])
}

func integrationValuationReportList(t *testing.T) (string, []any) {
	t.Helper()
	candidates := integrationProductCandidates(t)

	for _, productID := range candidates {
		reportList := runIntegrationCommand(
			t,
			[]string{"get", "valuation-report-list", "--payload", integrationPayload(t, map[string]any{
				"product_id_or_name": productID,
				"fields":             []string{"valuation_report_id", "date", "file_name", "source"},
				"limit":              1,
			})},
		)
		reports, ok := reportList.Data["valuation_reports"].([]any)
		if !ok {
			t.Fatalf("valuation-report-list should return data.valuation_reports[]: %#v", reportList.Data)
		}
		if len(reports) > 0 {
			return productID, reports
		}
	}
	return "", nil
}

func integrationFirstNonEmptyEventList(t *testing.T, command string, listField string, basePayload map[string]any) (string, []any) {
	t.Helper()
	for _, productID := range integrationProductCandidates(t) {
		doc := cloneTestMap(basePayload)
		doc["product_id_or_name"] = productID
		parts := strings.Fields(command)
		eventList := runIntegrationCommand(t, []string{parts[0], parts[1], "--payload", integrationPayload(t, doc)})
		events, ok := eventList.Data[listField].([]any)
		if !ok {
			t.Fatalf("%s should return data.%s[]: %#v", command, listField, eventList.Data)
		}
		if len(events) > 0 {
			return productID, events
		}
	}
	return "", nil
}

func integrationProductCandidates(t *testing.T) []string {
	t.Helper()
	candidates := make([]string, 0)
	seen := map[string]bool{}
	addCandidate := func(productID string) {
		productID = strings.TrimSpace(productID)
		if productID == "" || seen[productID] {
			return
		}
		seen[productID] = true
		candidates = append(candidates, productID)
	}
	productList := runIntegrationCommand(
		t,
		[]string{"get", "product-list", "--payload", `{"fields":["id","name"],"limit":20}`},
	)
	if products, ok := productList.Data["products"].([]any); ok {
		for _, item := range products {
			product, ok := item.(map[string]any)
			if !ok {
				continue
			}
			addCandidate(stringValue(product["id"]))
		}
	}
	return candidates
}

func integrationBalanceWithPositions(t *testing.T, balanceDate string, fields []string) (string, cliEnvelope) {
	t.Helper()
	for _, productID := range integrationProductCandidates(t) {
		payload := map[string]any{
			"product_like_id_or_name": productID,
			"fields":                  fields,
		}
		if balanceDate != "" {
			payload["date"] = balanceDate
		}
		balance, ok := runIntegrationOptionalCommand(t, []string{"get", "balance", "--payload", integrationPayload(t, payload)})
		if !ok {
			continue
		}
		if len(integrationPositionItems(balance.Data["positions"])) > 0 {
			t.Logf("balance position checks using product %s", productID)
			return productID, balance
		}
	}
	return "", cliEnvelope{}
}

func integrationPositionItems(value any) []any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	return items
}

func integrationTradingAnalysisCase(t *testing.T, startDate string, endDate string) (string, map[string]any, map[string]any) {
	t.Helper()
	for _, productID := range integrationProductCandidates(t) {
		tradingList, ok := runIntegrationOptionalArrayCommand(
			t,
			[]string{"get", "trading-analysis-list", "--payload", integrationPayload(t, map[string]any{
				"product_like_id_or_name": productID,
				"start_date":              startDate,
				"end_date":                endDate,
			})},
		)
		if !ok || len(tradingList.Data) == 0 {
			continue
		}
		for _, item := range tradingList.Data {
			trade, ok := item.(map[string]any)
			if !ok {
				t.Fatalf("trading-analysis-list item should be object: %#v", item)
			}
			if firstStringFromMap(trade, "order_book_id") == "" ||
				firstStringFromMap(trade, "asset_class") == "" ||
				firstStringFromMap(trade, "direction") == "" {
				continue
			}
			detail, ok := runIntegrationOptionalAnyCommand(t, []string{"get", "trading-analysis", "--payload", integrationPayload(t, map[string]any{
				"product_like_id_or_name": productID,
				"start_date":              startDate,
				"end_date":                endDate,
				"order_book_id":           trade["order_book_id"],
				"asset_class":             trade["asset_class"],
				"direction":               trade["direction"],
			})})
			if !ok {
				continue
			}
			detailData, ok := detail.Data.(map[string]any)
			if !ok {
				continue
			}
			t.Logf("trading-analysis checks using product %s and order_book_id %s", productID, trade["order_book_id"])
			return productID, trade, detailData
		}
	}
	return "", nil, nil
}

func cloneTestMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

type integrationEnv struct {
	baseURL   string
	username  string
	password  string
	workspace string
	profile   string
}

func requireIntegrationEnv(t *testing.T) integrationEnv {
	t.Helper()
	return integrationEnv{
		baseURL:   defaultIntegrationBaseURL,
		username:  defaultIntegrationUsername,
		password:  defaultIntegrationPassword,
		workspace: defaultIntegrationWorkspace,
		profile:   defaultIntegrationProfile,
	}
}

func integrationMutationProductPayload(name string) map[string]any {
	return map[string]any{
		"name":                name,
		"report_name":         name,
		"start_date":          integrationMutationStartDate,
		"trading_start_date":  integrationMutationStartDate,
		"data_source":         "trade_and_valuation_report",
		"investment_category": "equity",
		"strategy_category":   "stock_long",
		"benchmark":           map[string]any{"type": "index", "id": "000300.XSHG"},
		"calendar":            "exchange",
		"unit_policy":         "manual",
		"accounts": []map[string]any{{
			"name":           "stock",
			"is_custodian":   false,
			"account_number": "rqamsc-integration",
			"broker":         "ricequant",
		}},
		"fee_settings": map[string]any{
			"management_fee":        0,
			"custodian_fee":         0,
			"operation_fee":         0,
			"sales_and_service_fee": 0,
		},
	}
}

func cleanupStaleIntegrationMutationProducts(t *testing.T) {
	t.Helper()
	list := runIntegrationCommand(t, []string{"get", "product-list", "--payload", integrationPayload(t, map[string]any{
		"fields": []string{"id", "name"},
		"limit":  500,
	})})
	for _, item := range extractList(list.Data, "products") {
		product, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := stringValue(product["name"])
		productID := stringValue(product["id"])
		if productID != "" && strings.HasPrefix(name, integrationMutationProductPrefix) {
			if strings.HasPrefix(name, integrationMutationProductPrefix+"paper_trading_") {
				runIntegrationCleanupCommand(t, []string{"delete", "paper-trading", "--payload", integrationPayload(t, map[string]any{"product_id_or_name": productID})})
				continue
			}
			runIntegrationCleanupCommand(t, []string{"delete", "product", "--payload", integrationPayload(t, map[string]any{"product_id": productID})})
		}
	}
}

func integrationProductIDByName(t *testing.T, name string) string {
	t.Helper()
	list := runIntegrationCommand(t, []string{"get", "product-list", "--payload", integrationPayload(t, map[string]any{
		"fields": []string{"id", "name"},
		"limit":  500,
	})})
	for _, item := range extractList(list.Data, "products") {
		product, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if stringValue(product["name"]) == name {
			return stringValue(product["id"])
		}
	}
	return ""
}

func testIntegrationMutableTrade(t *testing.T, productID string, suffix string) {
	t.Helper()
	foreignID := "rqamsc_it_trade_" + suffix
	remarks := "rqamsc mutable integration trade " + suffix
	tradeDeleted := false
	runIntegrationAnyCommand(t, []string{"insert", "trade", "--payload", integrationPayload(t, map[string]any{
		"product_id": productID,
		"trades": []map[string]any{{
			"transaction_type": "buy",
			"datetime":         integrationMutationTradeDate + " 09:31:00",
			"trading_date":     integrationMutationTradeDate,
			"order_book_id":    "000001.XSHE",
			"symbol":           "PINGANBANK",
			"quantity":         100,
			"price":            10.5,
			"account":          "stock",
			"foreign_id":       foreignID,
			"remarks":          remarks,
		}},
	})})
	t.Cleanup(func() {
		if tradeDeleted {
			return
		}
		runIntegrationCleanupCommand(t, []string{"delete", "trade", "--payload", integrationPayload(t, map[string]any{
			"product_id":         productID,
			"start_date":         integrationMutationTradeDate,
			"end_date":           integrationMutationTradeDate,
			"sources":            []string{"open_api"},
			"account_names":      []string{"stock"},
			"is_query_assistant": true,
		})})
	})

	list := runIntegrationCommand(t, []string{"get", "trade-list", "--payload", integrationPayload(t, map[string]any{
		"product_id": productID,
		"start_date": integrationMutationTradeDate,
		"end_date":   integrationMutationTradeDate,
		"sources":    []string{"open_api"},
		"remarks":    remarks,
		"limit":      10,
	})})
	trade := firstIntegrationListObject(list.Data, "trades", func(item map[string]any) bool {
		return stringValue(item["foreign_id"]) == foreignID || stringValue(item["remarks"]) == remarks
	})
	if trade == nil {
		t.Fatalf("inserted trade was not returned by get trade-list: %#v", list.Data)
	}
	if tradeID := firstStringFromMap(trade, "id", "_id", "trade_id"); tradeID != "" {
		runIntegrationCommand(t, []string{"delete", "trade", "--payload", integrationPayload(t, map[string]any{
			"product_id": productID,
			"trade_ids":  []string{tradeID},
		})})
		tradeDeleted = true
		return
	}
	runIntegrationCommand(t, []string{"delete", "trade", "--payload", integrationPayload(t, map[string]any{
		"product_id":    productID,
		"start_date":    integrationMutationTradeDate,
		"end_date":      integrationMutationTradeDate,
		"sources":       []string{"open_api"},
		"account_names": []string{"stock"},
	})})
	tradeDeleted = true
}

func testIntegrationMutableSettlementTrade(t *testing.T, productID string) {
	t.Helper()
	uploadPath := filepath.Join(t.TempDir(), "settlement.csv")
	if err := os.WriteFile(uploadPath, []byte("date,account\n"+integrationMutationTradeDate+",stock\n"), 0o600); err != nil {
		t.Fatalf("failed to write settlement trade fixture: %v", err)
	}
	settlementDeleted := false
	t.Cleanup(func() {
		if settlementDeleted {
			return
		}
		runIntegrationCleanupCommand(t, []string{"delete", "trade", "--payload", integrationPayload(t, map[string]any{
			"product_id":         productID,
			"start_date":         integrationMutationTradeDate,
			"end_date":           integrationMutationTradeDate,
			"sources":            []string{"settlement_upload"},
			"account_names":      []string{"stock"},
			"is_query_assistant": true,
		})})
	})

	runIntegrationCommand(t, []string{"insert", "settlement-trade", "--payload", integrationPayload(t, map[string]any{
		"product_id":   productID,
		"account_name": "stock",
		"file_paths":   []string{uploadPath},
	})})
	runIntegrationCleanupCommand(t, []string{"delete", "trade", "--payload", integrationPayload(t, map[string]any{
		"product_id":    productID,
		"start_date":    integrationMutationTradeDate,
		"end_date":      integrationMutationTradeDate,
		"sources":       []string{"settlement_upload"},
		"account_names": []string{"stock"},
	})})
	settlementDeleted = true
}

func testIntegrationMutableCustodianEvent(t *testing.T, productID string) {
	t.Helper()
	event := map[string]any{
		"date":                 integrationMutationCustodianDate,
		"effective_date":       integrationMutationCustodianDate,
		"custodian_event_type": "product_dividend_paid",
		"amount":               1000,
	}
	runIntegrationCommand(t, []string{"insert", "custodian-event", "--payload", integrationPayload(t, map[string]any{
		"product_id":         productID,
		"custodian_events":   []map[string]any{event},
		"mutation_operation": "insert",
	})})

	eventID := integrationEventIDByDate(t, "get custodian-event-list", productID, "custodian_events", integrationMutationCustodianDate)
	if eventID == "" {
		t.Fatalf("inserted custodian event was not returned by get custodian-event-list")
	}
	eventDeleted := false
	t.Cleanup(func() {
		if eventDeleted {
			return
		}
		runIntegrationCleanupCommand(t, []string{"delete", "custodian-event", "--payload", integrationPayload(t, map[string]any{
			"product_id": productID,
			"event_ids":  []string{eventID},
		})})
	})

	updated := cloneTestMap(event)
	updated["amount"] = 1200
	runIntegrationCommand(t, []string{"update", "custodian-event", "--payload", integrationPayload(t, map[string]any{
		"product_id":         productID,
		"event_id":           eventID,
		"custodian_event":    updated,
		"mutation_operation": "update",
	})})
	runIntegrationCommand(t, []string{"delete", "custodian-event", "--payload", integrationPayload(t, map[string]any{
		"product_id": productID,
		"event_ids":  []string{eventID},
	})})
	eventDeleted = true
}

func testIntegrationMutableUnitEvent(t *testing.T, productID string) {
	t.Helper()
	event := map[string]any{
		"date":               integrationMutationUnitDate,
		"subscription_units": 1000,
	}
	runIntegrationCommand(t, []string{"insert", "unit-event", "--payload", integrationPayload(t, map[string]any{
		"product_id":  productID,
		"unit_events": []map[string]any{event},
	})})

	eventID := integrationEventIDByDate(t, "get unit-event-list", productID, "daily_units", integrationMutationUnitDate)
	if eventID == "" {
		t.Fatalf("inserted unit event was not returned by get unit-event-list")
	}
	eventDeleted := false
	t.Cleanup(func() {
		if eventDeleted {
			return
		}
		runIntegrationCleanupCommand(t, []string{"delete", "unit-event", "--payload", integrationPayload(t, map[string]any{
			"product_id": productID,
			"event_ids":  []string{eventID},
		})})
	})

	runIntegrationCommand(t, []string{"update", "unit-event", "--payload", integrationPayload(t, map[string]any{
		"product_id": productID,
		"event_id":   eventID,
		"unit_event": map[string]any{
			"date":               integrationMutationUnitDate,
			"subscription_units": 2000,
			"redemption_units":   nil,
		},
	})})
	runIntegrationCommand(t, []string{"delete", "unit-event", "--payload", integrationPayload(t, map[string]any{
		"product_id": productID,
		"event_ids":  []string{eventID},
	})})
	eventDeleted = true
}

func testIntegrationMutableValuationReport(t *testing.T, productID string) {
	t.Helper()
	reportDeleted := false
	runIntegrationAnyCommand(t, []string{"insert", "valuation-report", "--payload", integrationPayload(t, map[string]any{
		"product_id":    productID,
		"replace_dates": []string{integrationMutationValuationDate},
		"valuation_reports": []map[string]any{{
			"date":               integrationMutationValuationDate,
			"total_equity":       1000000,
			"units":              1000000,
			"unit_net_value":     1,
			"acc_unit_net_value": 1,
			"positions":          []any{},
		}},
	})})
	t.Cleanup(func() {
		if reportDeleted {
			return
		}
		runIntegrationCleanupCommand(t, []string{"delete", "valuation-report", "--payload", integrationPayload(t, map[string]any{
			"product_id": productID,
			"dates":      []string{integrationMutationValuationDate},
		})})
	})

	list := runIntegrationCommand(t, []string{"get", "valuation-report-list", "--payload", integrationPayload(t, map[string]any{
		"product_id": productID,
		"start_date": integrationMutationValuationDate,
		"end_date":   integrationMutationValuationDate,
		"fields":     []string{"valuation_report_id", "date", "source"},
		"limit":      5,
	})})
	report := firstIntegrationListObject(list.Data, "valuation_reports", func(item map[string]any) bool {
		return stringValue(item["date"]) == integrationMutationValuationDate
	})
	if report == nil {
		t.Fatalf("inserted valuation report was not returned by get valuation-report-list: %#v", list.Data)
	}

	runIntegrationCommand(t, []string{"delete", "valuation-report", "--payload", integrationPayload(t, map[string]any{
		"product_id": productID,
		"dates":      []string{integrationMutationValuationDate},
	})})
	reportDeleted = true
}

func testIntegrationMutablePaperTrading(t *testing.T, suffix string) {
	t.Helper()
	productName := integrationMutationProductPrefix + "paper_trading_" + suffix
	created := runIntegrationCommand(t, []string{"insert", "paper-trading", "--payload", integrationPayload(t, map[string]any{
		"template":      "equity_long",
		"name":          productName,
		"benchmark":     "index,000300.XSHG",
		"start_date":    integrationMutationStartDate,
		"init_amount":   1000000,
		"algo":          "open",
		"description":   "rqamsc mutable integration paper trading",
		"slippage_rate": 0,
	})})
	productID := firstStringFromMap(created.Data, "product_id")
	if productID == "" {
		productID = integrationProductIDByName(t, productName)
	}
	if productID == "" {
		t.Fatalf("insert paper-trading did not return a product id and product-list could not find %q: %#v", productName, created.Data)
	}

	paperTradingDeleted := false
	t.Cleanup(func() {
		if !paperTradingDeleted {
			runIntegrationCleanupCommand(t, []string{"delete", "paper-trading", "--payload", integrationPayload(t, map[string]any{
				"product_id_or_name": productID,
			})})
		}
	})

	detail := runIntegrationCommand(t, []string{"get", "paper-trading", "--payload", integrationPayload(t, map[string]any{
		"product_id_or_name": productID,
	})})
	if detailProductID := stringValue(detail.Data["product_id"]); detailProductID != "" && detailProductID != productID {
		t.Fatalf("created paper-trading product_id mismatch, want %s, got %s: %#v", productID, detailProductID, detail.Data)
	}
	assertIntegrationFields(t, "created paper-trading detail", detail.Data, []string{"product_id", "strategy_model"})
	assertIntegrationNoFields(t, "created paper-trading internal fields", detail.Data, []string{"_id", "version"})

	runIntegrationOptionalCommand(t, []string{"recompute", "paper-trading", "--payload", integrationPayload(t, map[string]any{
		"product_id_or_name": productID,
		"date":               integrationMutationStartDate,
	})})

	signalPath := filepath.Join(t.TempDir(), "signals.csv")
	if err := os.WriteFile(signalPath, []byte("date,order_book_id,target_weight\n"+integrationMutationStartDate+",000001.XSHE,1\n"), 0o600); err != nil {
		t.Fatalf("failed to write paper-trading signal fixture: %v", err)
	}
	if _, ok := runIntegrationOptionalCommand(t, []string{"insert", "paper-trading-signal", "--payload", integrationPayload(t, map[string]any{
		"product_id_or_name": productID,
		"file_paths":         []string{signalPath},
	})}); ok {
		runIntegrationOptionalCommand(t, []string{"delete", "paper-trading-signal", "--payload", integrationPayload(t, map[string]any{
			"product_id_or_name": productID,
			"start_date":         integrationMutationStartDate,
			"end_date":           integrationMutationStartDate,
		})})
	}

	runIntegrationOptionalCommand(t, []string{"update", "paper-trading", "--payload", integrationPayload(t, map[string]any{
		"product_id_or_name": productID,
		"update_fields": map[string]any{
			"algo": "open",
		},
	})})

	runIntegrationCommand(t, []string{"delete", "paper-trading", "--payload", integrationPayload(t, map[string]any{"product_id_or_name": productID})})
	paperTradingDeleted = true
}

func integrationEventIDByDate(t *testing.T, command string, productID string, listField string, date string) string {
	t.Helper()
	parts := strings.Fields(command)
	list := runIntegrationCommand(t, []string{parts[0], parts[1], "--payload", integrationPayload(t, map[string]any{
		"product_id":         productID,
		"start_date":         date,
		"end_date":           date,
		"include_auto_units": true,
		"fields":             []string{"id", "date", "source", "custodian_event_type", "amount", "subscription_units", "redemption_units"},
		"limit":              10,
	})})
	event := firstIntegrationListObject(list.Data, listField, func(item map[string]any) bool {
		return stringValue(item["date"]) == date && stringValue(item["source"]) != "auto"
	})
	if event == nil {
		return ""
	}
	return firstStringFromMap(event, "id", "_id", "event_id")
}

func firstIntegrationListObject(data any, listField string, match func(map[string]any) bool) map[string]any {
	for _, item := range extractList(data, listField) {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if match == nil || match(object) {
			return object
		}
	}
	return nil
}

func firstPositionStatementLocator(items []any) (string, string, string) {
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		productID := firstStringFromMap(object, "product_id", "product", "product_like_id")
		assetUnitID := firstStringFromMap(object, "asset_unit_id", "asset_unit")
		date := firstStringFromMap(object, "date", "latest_date", "statement_date", "trading_date")
		if productID != "" && assetUnitID != "" {
			return productID, assetUnitID, date
		}
	}
	return "", "", ""
}

func integrationDownloadPath(data map[string]any) string {
	return firstStringFromMap(data, "saved_path", "path", "file_path")
}

func runIntegrationCleanupCommand(t *testing.T, args []string) {
	t.Helper()
	var stdout strings.Builder
	var stderr strings.Builder
	code := Run(args, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Logf("cleanup Run(%v) returned code %d\nstdout: %s\nstderr: %s", redactIntegrationArgs(args), code, stdout.String(), stderr.String())
	}
}

func integrationPayload(t *testing.T, doc map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("failed to marshal integration payload: %v", err)
	}
	return string(raw)
}

func assertIntegrationFields(t *testing.T, label string, object map[string]any, fields []string) {
	t.Helper()
	for _, field := range fields {
		if _, ok := object[field]; !ok {
			t.Fatalf("%s missing field %q: %#v", label, field, object)
		}
	}
}

func assertIntegrationNoFields(t *testing.T, label string, object map[string]any, fields []string) {
	t.Helper()
	for _, field := range fields {
		if _, ok := object[field]; ok {
			t.Fatalf("%s should not expose field %q: %#v", label, field, object)
		}
	}
}

func assertIntegrationPositionFields(t *testing.T, value any) {
	t.Helper()
	if object, ok := value.(map[string]any); ok && len(object) == 0 {
		t.Log("positions is an empty object; per-position field checks skipped")
		return
	}
	positions, ok := value.([]any)
	if !ok {
		t.Fatalf("positions should be an array or empty object in flat balance response: %#v", value)
	}
	if len(positions) == 0 {
		t.Log("positions[] is empty; per-position field checks skipped")
		return
	}
	first, ok := positions[0].(map[string]any)
	if !ok {
		t.Fatalf("positions[0] should be an object: %#v", positions[0])
	}
	assertIntegrationFields(t, "positions[0] documented fields", first, []string{
		"order_book_id", "symbol", "asset_class", "direction", "quantity", "market_value",
	})
}

type cliAnyEnvelope struct {
	OK       bool           `json:"ok"`
	Command  string         `json:"command"`
	Data     any            `json:"data"`
	Metadata map[string]any `json:"metadata"`
	Error    map[string]any `json:"error"`
}

func runIntegrationAnyCommand(t *testing.T, args []string) cliAnyEnvelope {
	t.Helper()
	var stdout strings.Builder
	var stderr strings.Builder
	code := Run(args, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(%v) returned code %d\nstdout: %s\nstderr: %s", redactIntegrationArgs(args), code, stdout.String(), stderr.String())
	}
	var envelope cliAnyEnvelope
	if err := json.Unmarshal([]byte(stdout.String()), &envelope); err != nil {
		t.Fatalf("Run(%v) returned invalid JSON: %v\nstdout: %s", redactIntegrationArgs(args), err, stdout.String())
	}
	if !envelope.OK {
		t.Fatalf("Run(%v) returned failure envelope: %#v", redactIntegrationArgs(args), envelope)
	}
	return envelope
}

func integrationListData(t *testing.T, label string, data any, wrappedField string) []any {
	t.Helper()
	if items, ok := data.([]any); ok {
		return items
	}
	object, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("%s should return an array or object containing %s[]: %#v", label, wrappedField, data)
	}
	items, ok := object[wrappedField].([]any)
	if !ok {
		t.Fatalf("%s should return data.%s[]: %#v", label, wrappedField, object)
	}
	return items
}

func runIntegrationCommand(t *testing.T, args []string) cliEnvelope {
	t.Helper()
	var stdout strings.Builder
	var stderr strings.Builder
	code := Run(args, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(%v) returned code %d\nstdout: %s\nstderr: %s", redactIntegrationArgs(args), code, stdout.String(), stderr.String())
	}
	var envelope cliEnvelope
	if err := json.Unmarshal([]byte(stdout.String()), &envelope); err != nil {
		t.Fatalf("Run(%v) returned invalid JSON: %v\nstdout: %s", redactIntegrationArgs(args), err, stdout.String())
	}
	if !envelope.OK {
		t.Fatalf("Run(%v) returned failure envelope: %#v", redactIntegrationArgs(args), envelope)
	}
	return envelope
}

func runIntegrationOptionalCommand(t *testing.T, args []string) (cliEnvelope, bool) {
	t.Helper()
	var stdout strings.Builder
	var stderr strings.Builder
	code := Run(args, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Logf("optional Run(%v) returned code %d\nstdout: %s\nstderr: %s", redactIntegrationArgs(args), code, stdout.String(), stderr.String())
		return cliEnvelope{}, false
	}
	var envelope cliEnvelope
	if err := json.Unmarshal([]byte(stdout.String()), &envelope); err != nil {
		t.Fatalf("Run(%v) returned invalid JSON: %v\nstdout: %s", redactIntegrationArgs(args), err, stdout.String())
	}
	if !envelope.OK {
		t.Logf("optional Run(%v) returned failure envelope: %#v", redactIntegrationArgs(args), envelope)
		return cliEnvelope{}, false
	}
	return envelope, true
}

func runIntegrationOptionalAnyCommand(t *testing.T, args []string) (cliAnyEnvelope, bool) {
	t.Helper()
	var stdout strings.Builder
	var stderr strings.Builder
	code := Run(args, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Logf("optional Run(%v) returned code %d\nstdout: %s\nstderr: %s", redactIntegrationArgs(args), code, stdout.String(), stderr.String())
		return cliAnyEnvelope{}, false
	}
	var envelope cliAnyEnvelope
	if err := json.Unmarshal([]byte(stdout.String()), &envelope); err != nil {
		t.Fatalf("Run(%v) returned invalid JSON: %v\nstdout: %s", redactIntegrationArgs(args), err, stdout.String())
	}
	if !envelope.OK {
		t.Logf("optional Run(%v) returned failure envelope: %#v", redactIntegrationArgs(args), envelope)
		return cliAnyEnvelope{}, false
	}
	return envelope, true
}

type cliArrayEnvelope struct {
	OK       bool           `json:"ok"`
	Command  string         `json:"command"`
	Data     []any          `json:"data"`
	Metadata map[string]any `json:"metadata"`
	Error    map[string]any `json:"error"`
}

func runIntegrationArrayCommand(t *testing.T, args []string) cliArrayEnvelope {
	t.Helper()
	var stdout strings.Builder
	var stderr strings.Builder
	code := Run(args, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(%v) returned code %d\nstdout: %s\nstderr: %s", redactIntegrationArgs(args), code, stdout.String(), stderr.String())
	}
	var envelope cliArrayEnvelope
	if err := json.Unmarshal([]byte(stdout.String()), &envelope); err != nil {
		t.Fatalf("Run(%v) returned invalid JSON: %v\nstdout: %s", redactIntegrationArgs(args), err, stdout.String())
	}
	if !envelope.OK {
		t.Fatalf("Run(%v) returned failure envelope: %#v", redactIntegrationArgs(args), envelope)
	}
	return envelope
}

func runIntegrationOptionalArrayCommand(t *testing.T, args []string) (cliArrayEnvelope, bool) {
	t.Helper()
	var stdout strings.Builder
	var stderr strings.Builder
	code := Run(args, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Logf("optional Run(%v) returned code %d\nstdout: %s\nstderr: %s", redactIntegrationArgs(args), code, stdout.String(), stderr.String())
		return cliArrayEnvelope{}, false
	}
	var envelope cliArrayEnvelope
	if err := json.Unmarshal([]byte(stdout.String()), &envelope); err != nil {
		t.Fatalf("Run(%v) returned invalid JSON: %v\nstdout: %s", redactIntegrationArgs(args), err, stdout.String())
	}
	if !envelope.OK {
		t.Logf("optional Run(%v) returned failure envelope: %#v", redactIntegrationArgs(args), envelope)
		return cliArrayEnvelope{}, false
	}
	return envelope, true
}

func runRawIntegrationCommand(t *testing.T, args []string) string {
	t.Helper()
	var stdout strings.Builder
	var stderr strings.Builder
	code := Run(args, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run(%v) returned code %d\nstdout: %s\nstderr: %s", redactIntegrationArgs(args), code, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func assertIntegrationNDJSON(t *testing.T, args []string, sourceCount int) {
	t.Helper()
	raw := strings.TrimSpace(runRawIntegrationCommand(t, args))
	if sourceCount == 0 {
		if raw != "" {
			t.Fatalf("expected empty NDJSON for empty source list, got %s", raw)
		}
		return
	}
	lines := strings.Split(raw, "\n")
	if len(lines) != 1 {
		t.Fatalf("expected one NDJSON line, got %d: %s", len(lines), raw)
	}
	var item map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &item); err != nil {
		t.Fatalf("invalid NDJSON line: %v\nline: %s", err, lines[0])
	}
	if strings.TrimSpace(stringValue(item["id"])) == "" {
		t.Fatalf("NDJSON item should include id: %#v", item)
	}
}

func redactIntegrationArgs(args []string) []string {
	redacted := append([]string(nil), args...)
	for i := 0; i < len(redacted)-1; i++ {
		if redacted[i] == "--payload" {
			redacted[i+1] = "<redacted>"
		}
	}
	return redacted
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
