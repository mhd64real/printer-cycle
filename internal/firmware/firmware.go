// Package firmware fetches the files a few printers cannot print without.
//
// The printers this is for hold no firmware of their own. They load it from the
// host over USB at every power-on, and nobody but the manufacturer may
// redistribute the file, so no distribution ships it. Until it is here the
// printer prints nothing and reports no error at all.
//
// Everything here exists to make the failures loud. A firmware fetch has more
// ways to go wrong than most things printer-cycle does: the box may be offline,
// the mirror may have moved, the archive may not contain what it should, the
// converter may not be installed, the destination may not be writable. Every one
// of those has to say which it was, because the alternative is a printer that
// silently prints nothing, which is the exact experience this project exists to
// end.
package firmware

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Source is where one model's firmware comes from and what it is called.
type Source struct {
	// Archive is the file to fetch, relative to the mirror.
	Archive string

	// Image is the file inside the archive, before conversion.
	Image string

	// File is what the printer driver looks for afterwards.
	File string
}

// sources is taken from /usr/sbin/getweb in the foo2zjs package.
//
// Note what that script now says: every original ftp.hp.com URL in it is
// commented out, and the live ones point at a third-party mirror. The
// manufacturer's own copies are gone. That is worth knowing before relying on
// this, and it is why a failure here has to name the mirror rather than say
// "download failed": the day that host disappears, whoever reads the message
// should be able to see what happened.
var sources = map[string]Source{
	"sihp1000.dl":  {Archive: "sihp1000.tar.gz", Image: "sihp1000.img", File: "sihp1000.dl"},
	"sihp1005.dl":  {Archive: "sihp1005.tar.gz", Image: "sihp1005.img", File: "sihp1005.dl"},
	"sihp1018.dl":  {Archive: "sihp1018.tar.gz", Image: "sihp1018.img", File: "sihp1018.dl"},
	"sihp1020.dl":  {Archive: "sihp1020.tar.gz", Image: "sihp1020.img", File: "sihp1020.dl"},
	"sihpP1005.dl": {Archive: "sihpP1005.tar.gz", Image: "sihpP1005.img", File: "sihpP1005.dl"},
	"sihpP1006.dl": {Archive: "sihpP1006.tar.gz", Image: "sihpP1006.img", File: "sihpP1006.dl"},
	"sihpP1505.dl": {Archive: "sihpP1505.tar.gz", Image: "sihpP1505.img", File: "sihpP1505.dl"},
}

// DefaultMirror is where the driver package fetches from today.
const DefaultMirror = "https://www.quirinux.org/printers"

// DefaultDir is where the driver looks. On Debian /lib is a symlink into /usr,
// so this is the same directory the foo2zjs package creates and leaves empty.
const DefaultDir = "/lib/firmware/hp"

// maxArchive bounds a download.
//
// These files are a few hundred kilobytes. The bound is not about them: it is
// so that a mirror that has become something else entirely cannot fill the disk
// of a Raspberry Pi before anybody notices.
const maxArchive = 16 << 20

// fetchTimeout bounds the whole fetch.
//
// Generous, because a household connection fetching from a volunteer mirror is
// not always quick, and short enough that somebody watching a spinner finds out
// within a minute rather than deciding the software has hung.
const fetchTimeout = 60 * time.Second

// Installer fetches and installs firmware.
type Installer struct {
	// Mirror, Dir and Converter are settable so this can be exercised without
	// a network, without root, and without the driver package installed.
	Mirror    string
	Dir       string
	Converter string

	HTTP *http.Client
}

// New returns an Installer with the real defaults.
func New() *Installer {
	return &Installer{
		Mirror:    DefaultMirror,
		Dir:       DefaultDir,
		Converter: "arm2hpdl",
		HTTP:      &http.Client{Timeout: fetchTimeout},
	}
}

// Installed reports whether a firmware file is already here.
func (in *Installer) Installed(file string) bool {
	info, err := os.Stat(filepath.Join(in.Dir, file))
	return err == nil && info.Size() > 0
}

// Install fetches one firmware file and puts it where the driver looks.
//
// Every error says which step failed and what to do about it. Nothing here is
// retried: a caller pressing a button again is a better retry than a loop
// nobody can see.
func (in *Installer) Install(ctx context.Context, file string) error {
	source, ok := sources[file]
	if !ok {
		return fmt.Errorf("printer-cycle does not know where to get %s", file)
	}

	if in.Installed(source.File) {
		return nil
	}

	// Checked before the download rather than after, so an hour on a slow
	// connection is not spent discovering that the result cannot be saved.
	if err := in.checkWritable(); err != nil {
		return err
	}
	if _, err := exec.LookPath(in.Converter); err != nil {
		return fmt.Errorf("the %s tool is missing, which is what turns the downloaded file into "+
			"something the printer will accept. It comes with the foo2zjs driver package",
			in.Converter)
	}

	archive, err := in.download(ctx, source.Archive)
	if err != nil {
		return err
	}

	image, err := extract(archive, source.Image)
	if err != nil {
		return err
	}

	return in.convert(ctx, image, source.File)
}

// download fetches the archive into memory, bounded.
func (in *Installer) download(ctx context.Context, name string) ([]byte, error) {
	url := strings.TrimSuffix(in.Mirror, "/") + "/" + name

	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot ask for %s: %w", url, err)
	}

	client := in.HTTP
	if client == nil {
		client = &http.Client{Timeout: fetchTimeout}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, describeNetworkFailure(url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s. The file this printer needs is not where the "+
			"driver package says it is, which usually means the mirror has moved",
			url, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxArchive+1))
	if err != nil {
		return nil, fmt.Errorf("the download from %s stopped part way through: %w", url, err)
	}
	if len(body) > maxArchive {
		return nil, fmt.Errorf("%s is larger than %d bytes, which a firmware file is not",
			url, maxArchive)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("%s returned nothing at all", url)
	}
	return body, nil
}

// describeNetworkFailure turns a transport error into something actionable.
//
// This is the stage's whole point. "dial tcp: lookup www.quirinux.org: no such
// host" is true and tells somebody on a home network nothing. Being unable to
// resolve a name, on a box whose job is to sit in a cupboard, almost always
// means it is not online.
func describeNetworkFailure(url string, err error) error {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Errorf("this machine could not look up %s, so it is probably not connected "+
			"to the internet. The firmware has to be downloaded once; after that the printer "+
			"works offline", dnsErr.Name)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("downloading from %s took longer than %s and was given up on. "+
			"The connection may be very slow, or the mirror may be down", url, fetchTimeout)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("%s did not answer in time, so this machine may be offline "+
			"or the mirror may be down", url)
	}
	return fmt.Errorf("could not reach %s, so this machine may be offline: %w", url, err)
}

// extract pulls one file out of a gzipped tar.
func extract(archive []byte, want string) ([]byte, error) {
	gz, err := gzip.NewReader(strings.NewReader(string(archive)))
	if err != nil {
		return nil, fmt.Errorf("what was downloaded is not a gzip archive, so the mirror is "+
			"serving something other than firmware: %w", err)
	}
	defer gz.Close()

	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the downloaded archive is damaged: %w", err)
		}

		// Matched on the base name. The archives put the file at the top level
		// today, and a path is not something to trust from a download.
		if filepath.Base(header.Name) != want {
			continue
		}
		image, err := io.ReadAll(io.LimitReader(reader, maxArchive))
		if err != nil {
			return nil, fmt.Errorf("could not read %s out of the archive: %w", want, err)
		}
		if len(image) == 0 {
			return nil, fmt.Errorf("%s in the archive is empty", want)
		}
		return image, nil
	}
	return nil, fmt.Errorf("the archive does not contain %s, so it is not the firmware this "+
		"printer needs", want)
}

// convert runs the image through arm2hpdl and installs the result.
//
// Written to a temporary file in the destination directory and renamed, so a
// fetch interrupted half way cannot leave a truncated firmware file behind for
// a printer to choke on.
func (in *Installer) convert(ctx context.Context, image []byte, file string) error {
	final := filepath.Join(in.Dir, file)

	tmp, err := os.CreateTemp(in.Dir, "."+file+".*")
	if err != nil {
		return fmt.Errorf("cannot write into %s: %w", in.Dir, err)
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()

	cmd := exec.CommandContext(ctx, in.Converter)
	cmd.Stdin = strings.NewReader(string(image))
	cmd.Stdout = tmp
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s could not convert the downloaded file: %w: %s",
			in.Converter, err, strings.TrimSpace(stderr.String()))
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("cannot finish writing %s: %w", final, err)
	}

	info, err := tmp.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return fmt.Errorf("%s produced an empty file from the download", in.Converter)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cannot finish writing %s: %w", final, err)
	}

	// Readable by everything, because the thing that pushes it into the printer
	// is a udev helper rather than this process.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return fmt.Errorf("cannot put the firmware in place at %s: %w", final, err)
	}
	return nil
}

// checkWritable says early, and in words, what a permission failure means.
func (in *Installer) checkWritable() error {
	if err := os.MkdirAll(in.Dir, 0o755); err != nil {
		return fmt.Errorf("cannot create %s, which is where printer firmware lives. "+
			"printer-cycle may not have permission to write there: %w", in.Dir, err)
	}

	probe, err := os.CreateTemp(in.Dir, ".writable-*")
	if err != nil {
		return fmt.Errorf("cannot write into %s, which is where printer firmware has to go. "+
			"printer-cycle does not have permission: %w", in.Dir, err)
	}
	probe.Close()
	os.Remove(probe.Name())
	return nil
}
