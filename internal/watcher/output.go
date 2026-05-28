package watcher

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"time"
)

// OutputFormat selects where and how transfer records are written.
type OutputFormat int

const (
	FormatStdout   OutputFormat = iota // print to terminal
	FormatCSV                          // write to a CSV file
	FormatMarkdown                     // write to a Markdown table file
)

// transferRecord is the in-memory representation of a single Transfer event passed to output writers.
type transferRecord struct {
	Block     uint64
	Timestamp time.Time
	TxHash    string
	From      string
	To        string
	Amount    float64
	Symbol    string
}

// transferWriter is the sink that receives decoded transfer records for formatting and output.
type transferWriter interface {
	write(r transferRecord) error
	close() error
}

// newTransferWriter constructs the appropriate transferWriter for the given format, opening a file at path when needed.
func newTransferWriter(format OutputFormat, path string) (transferWriter, error) {
	switch format {
	case FormatCSV:
		return newCSVWriter(path)
	case FormatMarkdown:
		return newMarkdownWriter(path)
	default:
		return &stdoutWriter{}, nil
	}
}

// stdoutWriter prints each transfer record to standard output.
type stdoutWriter struct{}

func (w *stdoutWriter) write(r transferRecord) error {
	fmt.Printf("block=%-9d  time=%s  tx=%s\n  from=%s\n  to  =%s\n  amount=%.2f %s\n",
		r.Block, r.Timestamp.UTC().Format(time.RFC3339), r.TxHash, r.From, r.To, r.Amount, r.Symbol)
	return nil
}

func (w *stdoutWriter) close() error { return nil }

// csvWriter writes transfer records to a CSV file, with a header row written on construction.
type csvWriter struct {
	f   *os.File
	csv *csv.Writer
}

// newCSVWriter creates the file at path and writes the CSV header row.
func newCSVWriter(path string) (*csvWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := csv.NewWriter(f)
	if err := w.Write([]string{"block", "timestamp", "tx_hash", "from", "to", "amount", "token"}); err != nil {
		f.Close()
		return nil, err
	}
	w.Flush()
	return &csvWriter{f: f, csv: w}, nil
}

func (w *csvWriter) write(r transferRecord) error {
	return w.csv.Write([]string{
		strconv.FormatUint(r.Block, 10),
		r.Timestamp.UTC().Format(time.RFC3339),
		r.TxHash,
		r.From,
		r.To,
		fmt.Sprintf("%.6f", r.Amount),
		r.Symbol,
	})
}

func (w *csvWriter) close() error {
	w.csv.Flush()
	return w.f.Close()
}

// markdownWriter appends transfer records as rows to a GitHub-flavoured Markdown table.
type markdownWriter struct {
	f *os.File
}

// newMarkdownWriter creates the file at path and writes the Markdown table header and separator.
func newMarkdownWriter(path string) (*markdownWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	header := "| Block | Timestamp | Transaction Hash | From | To | Amount | Token |\n" +
		"|------:|:----------|:-----------------|:-----|:---|-------:|:------|\n"
	if _, err := fmt.Fprint(f, header); err != nil {
		f.Close()
		return nil, err
	}
	return &markdownWriter{f: f}, nil
}

func (w *markdownWriter) write(r transferRecord) error {
	_, err := fmt.Fprintf(w.f, "| %d | `%s` | `%s` | `%s` | `%s` | %.2f | %s |\n",
		r.Block, r.Timestamp.UTC().Format(time.RFC3339), r.TxHash, r.From, r.To, r.Amount, r.Symbol)
	return err
}

func (w *markdownWriter) close() error {
	return w.f.Close()
}
