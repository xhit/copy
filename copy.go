package copy

import (
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"
)

type timespec struct {
	Mtime time.Time
	Atime time.Time
	Ctime time.Time
}

// Copy copies src to dest, doesn't matter if src is a directory or a file.
func Copy(src, dest string, opt ...Options) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	return switchboard(src, dest, info, assureOptions(src, dest, opt...))
}

// switchboard switches proper copy functions regarding file type, etc...
// If there would be anything else here, add a case to this switchboard.
func switchboard(src, dest string, info os.FileInfo, opt Options) (err error) {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		err = onsymlink(src, dest, opt)
	case info.IsDir():
		err = dcopy(src, dest, info, opt)
	case info.Mode()&os.ModeNamedPipe != 0:
		err = pcopy(dest, info)
	default:
		err = fcopy(src, dest, info, opt)
	}

	return err
}

// copyNextOrSkip decide if this src should be copied or not.
// Because this "copy" could be called recursively,
// "info" MUST be given here, NOT nil.
func copyNextOrSkip(src, dest string, info os.FileInfo, opt Options) error {
	skip, err := opt.Skip(src)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	return switchboard(src, dest, info, opt)
}

// fcopy is for just a file,
// with considering existence of parent directory
// and file permission.
func fcopy(src, dest string, info os.FileInfo, opt Options) (err error) {

	if err = os.MkdirAll(filepath.Dir(dest), os.ModePerm); err != nil {
		return
	}

	f, err := os.Create(dest)
	if err != nil {
		return
	}
	defer fclose(f, &err)

	chmodfunc, err := opt.PermissionControl(info, dest)
	if err != nil {
		return err
	}
	chmodfunc(&err)

	s, err := os.Open(src)
	if err != nil {
		return
	}
	defer fclose(s, &err)

	var buf []byte = nil
	var w io.Writer = f
	// var r io.Reader = s
	if opt.CopyBufferSize != 0 {
		buf = make([]byte, opt.CopyBufferSize)
		// Disable using `ReadFrom` by io.CopyBuffer.
		// See https://github.com/otiai10/copy/pull/60#discussion_r627320811 for more details.
		w = struct{ io.Writer }{f}
		// r = struct{ io.Reader }{s}
	}
	if _, err = io.CopyBuffer(w, s, buf); err != nil {
		return err
	}

	if opt.Sync {
		err = f.Sync()
	}

	if opt.PreserveOwner {
		if err := preserveOwner(src, dest, info); err != nil {
			return err
		}
	}
	if opt.PreserveTimes {
		if err := preserveTimes(info, dest); err != nil {
			return err
		}
	}

	return
}

// dcopy is for a directory,
// with scanning contents inside the directory
// and pass everything to "copy" recursively.
func dcopy(srcdir, destdir string, info os.FileInfo, opt Options) (err error) {

	_, err = os.Stat(destdir)
	if err == nil && opt.OnDirExists != nil && destdir != opt.intent.dest {
		switch opt.OnDirExists(srcdir, destdir) {
		case Replace:
			if err := os.RemoveAll(destdir); err != nil {
				return err
			}
		case Untouchable:
			return nil
		} // case "Merge" is default behaviour. Go through.
	} else if err != nil && !os.IsNotExist(err) {
		return err // Unwelcome error type...!
	}

	// Make dest dir with 0755 so that everything writable.
	chmodfunc, err := opt.PermissionControl(info, destdir)
	if err != nil {
		return err
	}
	defer chmodfunc(&err)

	contents, err := ioutil.ReadDir(srcdir)
	if err != nil {
		return
	}

	for _, content := range contents {
		cs, cd := filepath.Join(srcdir, content.Name()), filepath.Join(destdir, content.Name())

		if err = copyNextOrSkip(cs, cd, content, opt); err != nil {
			// If any error, exit immediately
			return
		}
	}

	if opt.PreserveTimes {
		if err := preserveTimes(info, destdir); err != nil {
			return err
		}
	}

	if opt.PreserveOwner {
		if err := preserveOwner(srcdir, destdir, info); err != nil {
			return err
		}
	}

	return
}

func onsymlink(src, dest string, opt Options) error {
	switch opt.OnSymlink(src) {
	case Shallow:
		if err := lcopy(src, dest); err != nil {
			return err
		}
		if opt.PreserveTimes {
			return preserveLtimes(src, dest)
		}
		return nil
	case Deep:
		orig, err := os.Readlink(src)
		if err != nil {
			return err
		}
		info, err := os.Lstat(orig)
		if err != nil {
			return err
		}
		return copyNextOrSkip(orig, dest, info, opt)
	case Skip:
		fallthrough
	default:
		return nil // do nothing
	}
}

// lcopy is for a symlink,
// with just creating a new symlink by replicating src symlink.
func lcopy(src, dest string) error {
	src, err := os.Readlink(src)
	if err != nil {
		return err
	}
	return os.Symlink(src, dest)
}

// fclose ANYHOW closes file,
// with asiging error raised during Close,
// BUT respecting the error already reported.
func fclose(f *os.File, reported *error) {
	if err := f.Close(); *reported == nil {
		*reported = err
	}
}
