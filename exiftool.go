package exiftool

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

var binary = "exiftool"
var executeArg = "-execute"
var initArgs = []string{"-stay_open", "True", "-@", "-", "-common_args"}
var extractArgs = []string{"-j"}
var closeArgs = []string{"-stay_open", "False", executeArg}
var readyToken = []byte("{ready}\n")
var readyTokenLen = len(readyToken)

// Exiftool is the exiftool utility wrapper
type Exiftool struct {
	lock    sync.Mutex
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	scanout *bufio.Scanner
	buferr  bytes.Buffer
}

// FileMetadata is a structure that represents an exiftool extraction. File contains the
// filename that had to be extracted. If anything went wrong, Err will not be nil. Fields
// stores extracted fields.
type FileMetadata struct {
	File   string
	Fields map[string]interface{}
	Err    error
}

// NewExiftool instanciates a new Exiftool with configuration functions. If anything went
// wrong, a non empty error will be returned
func NewExiftool(opts ...func(*Exiftool) error) (*Exiftool, error) {
	e := Exiftool{}
	for _, opt := range opts {
		if err := opt(&e); err != nil {
			return nil, fmt.Errorf("error when configuring exiftool: %v", err)
		}
	}

	cmd := exec.Command(binary, initArgs...)
	cmd.Stderr = &e.buferr
	var err error
	if e.stdin, err = cmd.StdinPipe(); err != nil {
		return nil, fmt.Errorf("error when piping stdin: %v", err)
	}
	if e.stdout, err = cmd.StdoutPipe(); err != nil {
		return nil, fmt.Errorf("error when piping stdout: %v", err)
	}
	e.scanout = bufio.NewScanner(e.stdout)
	e.scanout.Split(splitReadyToken)

	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("error when executing commande: %v", err)
	}

	return &e, nil
}

// Close closes exiftool. If anything went wrong, a non empty error will be returned
func (e *Exiftool) Close() error {
	e.lock.Lock()
	defer e.lock.Unlock()
	for _, v := range closeArgs {
		fmt.Fprintln(e.stdin, v)
	}

	errs := []error{}
	if err := e.stdout.Close(); err != nil {
		errs = append(errs, fmt.Errorf("error while closing stdout: %v", err))
	}
	if err := e.stdin.Close(); err != nil {
		errs = append(errs, fmt.Errorf("error while closing stdin: %v", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("error while closing exiftool: %v", errs)
	}

	return nil
}

// ExtractMetadata extracts metadata from files
func (e *Exiftool) ExtractMetadata(files ...string) []FileMetadata {
	e.lock.Lock()
	defer e.lock.Unlock()

	fms := make([]FileMetadata, len(files))

	for i, f := range files {
		fms[i].File = f

		if _, err := os.Stat(f); err != nil {
			fms[i].Err = err
			continue
		}

		for _, curA := range extractArgs {
			fmt.Fprintln(e.stdin, curA)
		}
		fmt.Fprintln(e.stdin, f)
		fmt.Fprintln(e.stdin, executeArg)

		if !e.scanout.Scan() {
			fms[i].Err = fmt.Errorf("nothing on stdout")
			continue
		}
		if e.buferr.Len() > 0 {
			fms[i].Err = fmt.Errorf("error caught on stderr: %v", string(e.buferr.Bytes()))
			e.buferr.Reset()
			continue
		}
		if e.scanout.Err() != nil {
			fms[i].Err = fmt.Errorf("error while reading stdout: %v", e.scanout.Err())
			continue
		}

		var m []map[string]interface{}
		if err := json.Unmarshal(e.scanout.Bytes(), &m); err != nil {
			fms[i].Err = fmt.Errorf("error during json unmarshaling: %v", err)
			continue
		}
		fms[i].Fields = m[0]
	}

	return fms
}

func splitReadyToken(data []byte, atEOF bool) (int, []byte, error) {
	idx := bytes.Index(data, readyToken)
	if idx == -1 {
		if atEOF && len(data) > 0 {
			return 0, data, fmt.Errorf("no final token found")
		}
		return 0, nil, nil
	}
	return idx + readyTokenLen, data[:idx], nil
}
