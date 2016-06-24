// Command jsondir converts a directory structure to JSON.
//
// jsondir will walk a directory tree and convert its files to what it thinks is an appropriate JSON
// representation. Boolean values are true/TRUE and false/FALSE, numerics are any normal value
// handled by strconv.ParseInt, floats any string convertible by strconv.ParseFloat, the string
// "null" or "NULL" is a null value, and everything else is treated as a string.
//
// Files ending in an '@' (at sign) are treated as raw JSON values and will be unmarshaled upon
// loading to verify they're valid. Invalid data is a failure.
//
// If the -x flag is set, executable files will be run to generate JSON output. This can be used to
// nest jsondir calls if necessary (e.g., including a separate directory tree).
//
// By default, dot files are ignored.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode"
)

var logOutput io.Writer = ioutil.Discard
var errlog = log.New(os.Stderr, "jsondir: ", 0)

// SkipFile errors are returned by walk functions when a file is to be skipped. This can occur if
// the file is ignored, a symlink (when symlinks are ignored), or if the file was both executable
// and exited with a status code 65. Any other non-zero status is a failure.
type SkipFile string

func (s SkipFile) Error() string {
	return "skipping file entry " + string(s)
}

func isSkip(err error) bool {
	_, ok := err.(SkipFile)
	return ok
}

type prefixWriter struct {
	firstWrite bool
	prefix     []byte
	lb         byte
	w          io.Writer
}

func newPrefixWriter(w io.Writer, prefix string) *prefixWriter {
	return &prefixWriter{
		prefix: []byte("\n" + prefix),
		w:      w,
	}
}

func (p *prefixWriter) Write(b []byte) (n int, err error) {
	if p.w == ioutil.Discard {
		return len(b), nil
	}

	n = len(b)
	if n == 0 {
		return n, nil
	}

	if !p.firstWrite || p.lb == '\n' {
		req := len(p.prefix) + len(b) - 1
		buf := make([]byte, req)
		copy(buf[copy(buf, p.prefix[1:]):], b)
		b = buf
		p.firstWrite = true
	}

	lb := b[len(b)-1]
	numNLs := bytes.Count(b, p.prefix[:1])
	if lb == '\n' {
		numNLs--
	}

	if numNLs > 0 {
		b = bytes.Replace(b, p.prefix[:1], p.prefix, numNLs)
	}

	wn, err := p.w.Write(b)
	if wn > 0 {
		p.lb = b[wn-1]
	}

	if err != nil {
		return wn, err
	}

	if wn != len(b) {
		return wn, io.ErrShortWrite
	}

	return n, err
}

func readProc(name string, arg ...string) (out []byte, err error) {
	cmd := exec.Command(name, arg...)
	if !filepath.IsAbs(cmd.Path) {
		cmd.Path, err = filepath.Abs(cmd.Path)
		if err != nil {
			return nil, err
		}
	}

	// Create temporary directory for exec
	if !*noTmpExec {
		dir, err := ioutil.TempDir("", "jsondir-exec")
		if err != nil {
			return nil, err
		}
		cmd.Dir = dir
		defer func() {
			if rmerr := os.RemoveAll(dir); rmerr != nil {
				errlog.Print("unable to clean up temp directory ", dir, ": ", err)
			}
		}()
	} else if *relExec {
		cmd.Dir = filepath.Dir(cmd.Path)
	}

	stderr := newPrefixWriter(logOutput, name+": ")
	cmd.Stderr = stderr
	out, err = cmd.Output()

	if stderr.lb != '\n' && stderr.firstWrite {
		_, err := io.WriteString(os.Stderr, "\n")
		if err != nil {
			errlog.Print("unable to write newline to stderr (this will likely fail): ", err)
		}
	}

	switch e := err.(type) {
	case nil:
		return out, nil
	case *exec.ExitError:
		switch ps := e.Sys().(type) {
		case syscall.WaitStatus:
			code := ps.ExitStatus()
			if code != 0 {
				log.Print(name, ": exited with status ", code)
			}
			switch code {
			case 0:
				return out, nil
			case 65:
				return nil, SkipFile(name)
			default:
				return nil, err
			}
		default:
		}
	default:
		return nil, err
	}

	return out, err
}

func follow(loc string) error {
	if *followSymlinks {
		return nil
	}

	ls, err := os.Lstat(loc)
	if err != nil {
		return err
	}

	if ls.Mode()&os.ModeSymlink == os.ModeSymlink {
		return SkipFile(loc + " (symlink)")
	}

	return nil
}

func walkValue(fi os.FileInfo, loc string) (result interface{}, err error) {
	if err = follow(loc); err != nil {
		return nil, err
	}

	if fi == nil {
		fi, err = os.Stat(loc)
		if err != nil {
			return nil, err
		}
	}

	var data []byte
	switch {
	case fi.IsDir():
		return walkDir(fi, loc)
	case *allowExecute && fi.Mode()&0111 != 0: // Executable
		data, err = readProc(loc)
		if err != nil && !isSkip(err) {
			errlog.Print("error executing ", loc, ": ", err)
		}
	default:
		data, err = ioutil.ReadFile(loc)
	}

	if err != nil {
		return nil, err
	}

	if interpolated := strings.HasSuffix(fi.Name(), "@"); interpolated {
		// Have to unmarshal this instead of returning RawMessage to handle merging paths.
		err = json.Unmarshal(data, &result)
		return result, err
	}

	// null -> bool -> integer -> float64 -> string
	dstr := string(data)
	trimmed := strings.TrimRightFunc(dstr, unicode.IsSpace)
	if !*keepWhitespace {
		dstr = trimmed
	}

	switch dstr {
	case "null", "NULL":
		return nil, nil
	case "true", "TRUE":
		return true, nil
	case "false", "FALSE":
		return false, nil
	case "0":
		return int64(0), nil
	}

	if i64, err := strconv.ParseInt(trimmed, 0, 64); err == nil {
		return i64, nil
	}

	if f64, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return f64, nil
	}

	return dstr, nil
}

func walkDir(fi os.FileInfo, loc string) (result interface{}, err error) {
	isArray := strings.HasSuffix(loc, "[]")

	key := loc
	if isArray || strings.HasSuffix(loc, "{}") {
		key = key[:len(key)-2]
	}

	if key == "" {
		errlog.Print("skipping invalid file ", loc)
		return nil, SkipFile(loc)
	}

	info, err := ioutil.ReadDir(loc)
	if err != nil {
		return nil, err
	}

	var walk func(index int, path string, fi os.FileInfo) error

	if isArray {
		var ary []interface{}
		walk = func(i int, path string, fi os.FileInfo) error {
			obj, err := walkValue(fi, path)
			if err != nil {
				return err
			}

			ary = append(ary, obj)
			return nil
		}

		defer func() {
			if err == nil {
				result = ary
			}
		}()
	} else {
		var obj = make(map[string]interface{})
		walk = func(_ int, path string, fi os.FileInfo) (err error) {
			key := fi.Name()
			switch {
			case strings.HasSuffix(key, "@"): // Interpolated value
				key = key[:len(key)-1]
			case fi.IsDir() && strings.HasSuffix(key, "[]"): // Array
				key = key[:len(key)-2]
			case fi.IsDir() && strings.HasSuffix(key, "{}"): // Forced obj (e.g., if key ends in [])
				key = key[:len(key)-2]
			}

			if len(key) == 0 {
				return SkipFile(path)
			}

			r, err := walkValue(fi, path)
			if isSkip(err) {
				return nil
			} else if err != nil {
				return err
			}

			obj[key] = r
			return nil
		}

		defer func() {
			if err == nil {
				result = obj
			}
		}()
	}

	for i, fi := range info {
		path := filepath.Join(loc, fi.Name())
		if ignoreFile(path) {
			continue
		}

		err = walk(i, path, fi)
		if err != nil {
			if isSkip(err) {
				log.Print(err)
				continue
			}
			errlog.Print("unable to load file at path ", path, ": ", err)
			return nil, err
		}
	}

	return
}

type StringSet map[string]struct{}

func (ss StringSet) Has(v string) (ok bool) {
	_, ok = ss[v]
	return ok
}

func (ss StringSet) Set(v string) error {
	ss[v] = struct{}{}
	return nil
}

func (ss StringSet) Strings() (strs []string) {
	strs = make([]string, len(ss))
	i := 0
	for k := range ss {
		strs[i] = k
		i++
	}
	sort.Strings(strs)
	return strs
}

func (ss StringSet) String() string {
	return fmt.Sprint(ss.Strings())
}

var (
	ignorePatterns = make(StringSet)

	verbose        = flag.Bool("v", false, "Enable log messages.")
	compact        = flag.Bool("c", !isTTY(), "Whether to emit compact JSON.")
	followSymlinks = flag.Bool("s", false, "Whether to follow symlinks.")
	keepWhitespace = flag.Bool("ws", false, "Keep trailing whitespace in uninterpolated strings.")
	allowExecute   = flag.Bool("x", false, "Allow execution of executable files to generate content.")
	noTmpExec      = flag.Bool("nt", false, "Don't execute files from a temporary directory.")
	relExec        = flag.Bool("rx", false, "Execute files in their directory (instead of pwd or tmp - implies -nt).")
)

func init() {
	flag.Var(ignorePatterns, "i", "Specify a `pattern` to ignore. Uses filepath.Match. Defaults to files beginning with '.'.")
}

func ignoreFile(path string) bool {
	for k := range ignorePatterns {
		path := path
		if strings.IndexByte(k, os.PathSeparator) == -1 {
			path = filepath.Base(path)
		}
		if m, _ := filepath.Match(k, path); m {
			return true
		}
	}
	return false
}

func main() {
	log.SetPrefix("jsondir: ")
	log.SetFlags(0)

	flag.Parse()

	if *relExec {
		*noTmpExec = true
	}

	if *verbose {
		logOutput = os.Stderr
	}

	log.SetOutput(logOutput)

	if len(ignorePatterns) == 0 {
		ignorePatterns.Set(".*")
	}

	for s := range ignorePatterns {
		if s == "" {
			delete(ignorePatterns, s)
			continue
		}

		if _, err := filepath.Match(s, "."); err != nil {
			errlog.Fatalf("invalid ignore pattern %q: %v", s, err)
		}
	}

	for _, p := range flag.Args() {
		data, err := walkValue(nil, p)
		if isSkip(err) {
			log.Print(err)
			continue
		} else if err != nil {
			errlog.Fatal("unable to walk path ", p, ": ", err)
		}

		var b []byte
		if *compact {
			b, err = json.Marshal(data)
		} else {
			b, err = json.MarshalIndent(data, "", "\t")
		}
		if err != nil {
			errlog.Fatal("unable to marshal result ", p, ": ", err)
		}

		fmt.Printf("%s\n", b)
	}
}

// isTTY attempts to determine whether the current stdout refers to a terminal.
func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		errlog.Println("Error getting Stat of os.Stdout:", err)
		return true // Assume human readable
	}
	return (fi.Mode() & os.ModeNamedPipe) != os.ModeNamedPipe
}
