package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jaysongiroux/hook_way/internal/cli"
	"github.com/jaysongiroux/hook_way/internal/openapiimport"
)

func runImportOpenAPI(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("import-openapi", flag.ContinueOnError)
	spec := flags.String("spec", "", "local OpenAPI JSON/YAML `FILE`")
	settings := flags.String("settings", "", "import settings `FILE` with explicit operation selections")
	output := flags.String("out", "", "create new YAML `FILE` (default stdout; never overwrites)")
	list := flags.Bool("list", false, "inspect operations without generating configuration")
	jsonOut := flags.Bool("json", false, "print inspection as JSON (requires --list)")
	if code, ok := program.Parse("import-openapi", flags, args, stdout, stderr); !ok {
		return code
	}
	if *spec == "" || (*jsonOut && !*list) || (*list && (*settings != "" || *output != "")) || (!*list && *settings == "") {
		writeln(stderr, "import-openapi requires --spec and either --list [--json] or --settings [--out]")
		return cli.ExitUsage
	}
	data, err := readImportFile(*spec)
	if err != nil {
		writeln(stderr, err)
		return cli.ExitFail
	}
	if *list {
		report := openapiimport.Inspect(data)
		if *jsonOut {
			if err := json.NewEncoder(stdout).Encode(report); err != nil {
				writeln(stderr, "write inspection failed")
				return cli.ExitFail
			}
		} else {
			for _, op := range report.Operations {
				writef(stdout, "%s %s  tool=%s  operationId=%s\n", op.Method, op.Path, op.Name, op.OperationID)
				if security, ok := op.Security.([]any); ok && len(security) > 0 {
					writef(stdout, "  authentication: %s\n", mustJSON(op.Security))
				}
				printImportDiagnostics(stderr, op.Diagnostics)
			}
			printImportDiagnostics(stderr, report.Diagnostics)
		}
		for _, d := range report.Diagnostics {
			if d.Severity == "error" {
				return cli.ExitFail
			}
		}
		return cli.ExitOK
	}
	settingsData, err := readImportFile(*settings)
	if err != nil {
		writeln(stderr, err)
		return cli.ExitFail
	}
	opts, err := openapiimport.DecodeSettings(settingsData)
	if err != nil {
		writeln(stderr, err)
		return cli.ExitFail
	}
	result := openapiimport.Generate(data, opts)
	printImportDiagnostics(stderr, result.Diagnostics)
	if result.HasErrors() {
		return cli.ExitFail
	}
	if *output == "" {
		if _, err := stdout.Write(result.YAML); err != nil {
			writeln(stderr, "write configuration failed")
			return cli.ExitFail
		}
		return cli.ExitOK
	}
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		writeln(stderr, "cannot create output file (it may already exist)")
		return cli.ExitFail
	}
	_, writeErr := file.Write(result.YAML)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(*output)
		writeln(stderr, "write configuration failed")
		return cli.ExitFail
	}
	return cli.ExitOK
}

func readImportFile(path string) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- local input path explicitly supplied by the CLI operator; never read from an HTTP request.
	if err != nil {
		return nil, errors.New("cannot open import input file")
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, openapiimport.MaxDocumentBytes+1))
	if err != nil {
		return nil, errors.New("cannot read import input file")
	}
	if len(data) > openapiimport.MaxDocumentBytes {
		return nil, errors.New("import input exceeds 8 MiB")
	}
	return data, nil
}
func printImportDiagnostics(w io.Writer, ds []openapiimport.Diagnostic) {
	for _, d := range ds {
		_, _ = fmt.Fprintf(w, "%s: %s: %s\n", d.Severity, d.Location, d.Message)
	}
}
func mustJSON(v any) string { raw, _ := json.Marshal(v); return string(raw) }
