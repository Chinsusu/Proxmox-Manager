package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// outputFormat theo Phần II mục 14 ("CLI | Go, JSON/table output") —
// table mặc định cho người vận hành đọc trực tiếp, json cho automation/script.
type outputFormat string

const (
	outputTable outputFormat = "table"
	outputJSON  outputFormat = "json"
)

// printJSON in bất kỳ giá trị nào dạng JSON indent — dùng khi
// --output json hoặc khi bản thân dữ liệu không có bảng cột rõ ràng.
func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// printTable in một bảng đơn giản căn cột bằng tabwriter — header là
// dòng đầu, rows là các dòng tiếp theo, cùng số cột.
func printTable(header []string, rows [][]string) {
	if len(rows) == 0 {
		fmt.Println("(no results)")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, strings.Join(header, "\t"))
	for _, row := range rows {
		_, _ = fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	_ = w.Flush()
}

func printErr(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
}

// nilToDash trả "-" cho *string nil, dùng khi render bảng (tránh in
// "<nil>" khó đọc).
func nilToDash(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

func nilIntToDash(n *int) string {
	if n == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *n)
}
