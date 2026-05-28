package watcher

import (
	"encoding/csv"
	"fmt"
	"os"
)

type OutputFormat int

const (
	FormatStdout   OutputFormat = iota
	FormatCSV
	FormatMarkdown
)

type transferRecord struct {
	Block  uint64
	TxHash string
	From   string
	To     string
	Amount float64
	Symbol string
}

type transferWriter interface {
	write(r transferRecord) error
	close() error
}

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

// stdoutWriter

type stdoutWriter struct{}

func (w *stdoutWriter) write(r transferRecord) error {
	fmt.Printf("block=%-9d  tx=%s\n  from=%s\n  to  =%s\n  amount=%.2f %s\n",
		r.Block, r.TxHash, r.From, r.To, r.Amount, r.Symbol)
	return nil
}

func (w *stdoutWriter) close() error { return nil }

// csvWriter

type csvWriter struct {
	f   *os.File
	csv *csv.Writer
}

func newCSVWriter(path string) (*csvWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := csv.NewWriter(f)
	if err := w.Write([]string{"block", "tx_hash", "from", "to", "amount", "token"}); err != nil {
		f.Close()
		return nil, err
	}
	w.Flush()
	return &csvWriter{f: f, csv: w}, nil
}

func (w *csvWriter) write(r transferRecord) error {
	err := w.csv.Write([]string{
		fmt.Sprintf("%d", r.Block),
		r.TxHash,
		r.From,
		r.To,
		fmt.Sprintf("%.6f", r.Amount),
		r.Symbol,
	})
	if err != nil {
		return err
	}
	w.csv.Flush()
	return w.csv.Error()
}

func (w *csvWriter) close() error {
	w.csv.Flush()
	return w.f.Close()
}

// markdownWriter

type markdownWriter struct {
	f *os.File
}

func newMarkdownWriter(path string) (*markdownWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	header := "| Block | Transaction Hash | From | To | Amount | Token |\n" +
		"|------:|:-----------------|:-----|:---|-------:|:------|\n"
	if _, err := fmt.Fprint(f, header); err != nil {
		f.Close()
		return nil, err
	}
	return &markdownWriter{f: f}, nil
}

func (w *markdownWriter) write(r transferRecord) error {
	_, err := fmt.Fprintf(w.f, "| %d | `%s` | `%s` | `%s` | %.2f | %s |\n",
		r.Block, r.TxHash, r.From, r.To, r.Amount, r.Symbol)
	return err
}

func (w *markdownWriter) close() error {
	return w.f.Close()
}
