package native

import (
	"encoding/base64"
	"testing"
)

func TestReportExportDialogOptionsUseRequestedLocale(t *testing.T) {
	english, err := ParseReportExport("report.png", "image/png", validPNGBase64(), "en-US")
	if err != nil {
		t.Fatal(err)
	}
	englishOptions := reportExportDialogOptions(english)
	if englishOptions.Title != "Save test report" || englishOptions.Filters[0].DisplayName != "PNG image (*.png)" {
		t.Fatalf("English options = %#v", englishOptions)
	}

	chinese, err := ParseReportExport("report.json", "application/json", base64.StdEncoding.EncodeToString([]byte("{}")), "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	chineseOptions := reportExportDialogOptions(chinese)
	if chineseOptions.Title != "保存测试报告" || chineseOptions.Filters[0].DisplayName != "JSON 报告 (*.json)" {
		t.Fatalf("Chinese options = %#v", chineseOptions)
	}
}

func validPNGBase64() string {
	return "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
}
