package plugins

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

const maxPackageFile = int64(64 << 20)
const maxPackageExpanded = int64(512 << 20)
const maxPackageEntries = 10000

type packageMember struct {
	digest string
	mode   fs.FileMode
}

type packageExtraction struct {
	root    *os.Root
	members map[string]packageMember
	entries int
	total   int64
}

// PreparePackage validates every archive member before mapping selected files
// into an immutable staging directory. It never executes package scripts.
// From may identify one exact file, a subtree, or "." for the archive root.
// A raw asset is a single file whose source name is assetName.
func PreparePackage(archive, dest string, p Plugin, assetName string) (map[string]File, error) {
	if err := ValidatePlugin(p); err != nil {
		return nil, err
	}
	parent, name, err := acquisitionParent(dest)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	if err = packageEmptyDestination(parent, name); err != nil {
		return nil, err
	}
	sourceParent, sourceName, err := acquisitionParent(archive)
	if err != nil {
		return nil, err
	}
	defer sourceParent.Close()
	info, err := sourceParent.Lstat(sourceName)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxPluginDownload {
		return nil, errors.New("package must be a regular file of at most 256 MiB")
	}
	source, err := sourceParent.Open(sourceName)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	// Work directories live under the already-open staging parent, keeping every
	// operation confined even if a caller renames that parent during extraction.
	workName := ".plugin-unpack-" + rand.Text()
	if err = parent.Mkdir(workName, 0700); err != nil {
		return nil, err
	}
	defer parent.RemoveAll(workName)
	work, err := parent.OpenRoot(workName)
	if err != nil {
		return nil, err
	}
	defer work.Close()
	extracted := packageExtraction{root: work, members: map[string]packageMember{}}
	format := p.Format
	if format == "" || format == "auto" {
		switch {
		case strings.HasSuffix(strings.ToLower(assetName), ".zip"):
			format = "zip"
		case strings.HasSuffix(strings.ToLower(assetName), ".tar.gz"), strings.HasSuffix(strings.ToLower(assetName), ".tgz"):
			format = "tar.gz"
		default:
			format = "raw"
		}
	}
	switch format {
	case "zip":
		err = extracted.zip(source, info.Size())
	case "tar.gz":
		err = extracted.tar(source)
	case "raw":
		if !cleanRelative(assetName, false) || strings.Contains(assetName, "/") {
			return nil, errors.New("invalid raw asset filename")
		}
		err = extracted.file(assetName, info.Size(), 0644, source)
	default:
		err = errors.New("unsupported package format")
	}
	if err != nil {
		return nil, err
	}
	return extracted.publish(parent, name, p)
}

func (e *packageExtraction) publish(parent *os.Root, name string, p Plugin) (map[string]File, error) {
	targets, err := e.mapFiles(p)
	if err != nil {
		return nil, err
	}
	stageName := ".plugin-map-" + rand.Text()
	if err = parent.Mkdir(stageName, 0700); err != nil {
		return nil, err
	}
	defer parent.RemoveAll(stageName)
	stage, err := parent.OpenRoot(stageName)
	if err != nil {
		return nil, err
	}
	defer stage.Close()
	files := make(map[string]File, len(targets))
	names := make([]string, 0, len(targets))
	for target := range targets {
		names = append(names, target)
	}
	sort.Strings(names)
	for _, target := range names {
		from := targets[target]
		if err = stage.MkdirAll(path.Dir(target), 0755); err != nil {
			return nil, err
		}
		in, err := e.root.Open(from)
		if err != nil {
			return nil, err
		}
		member := e.members[from]
		out, err := stage.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, member.mode)
		if err != nil {
			in.Close()
			return nil, err
		}
		_, err = io.Copy(out, in)
		in.Close()
		if err == nil {
			err = out.Sync()
		}
		closeErr := out.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		files[target] = File{SHA256: member.digest, Seed: Protected(target, p.Preserve)}
	}
	if err = packageSyncDirectories(stage, names); err != nil {
		return nil, err
	}
	// Recheck immediately before publication; an existing nonempty destination
	// must never be replaced. Remove only an empty directory, never recursively.
	if err = packageEmptyDestination(parent, name); err != nil {
		return nil, err
	}
	if err = parent.Remove(name); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err = parent.Rename(stageName, name); err != nil {
		return nil, err
	}
	if err = packageSyncDirectories(parent, nil); err != nil {
		return nil, err
	}
	return files, nil
}

func packageEmptyDestination(parent *os.Root, name string) error {
	st, err := parent.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New("package destination must be a real empty directory")
	}
	dir, err := parent.Open(name)
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.Readdirnames(1)
	if err != nil && err != io.EOF {
		return err
	}
	if len(entries) != 0 {
		return errors.New("package destination must be empty")
	}
	return nil
}

func packageArchiveName(raw string, directory bool) (string, error) {
	if raw == "" || strings.HasPrefix(raw, "/") {
		return "", errors.New("unsafe archive member path")
	}
	name := strings.TrimPrefix(raw, "./")
	if directory {
		name = strings.TrimSuffix(name, "/")
	}
	if directory && (name == "" || name == ".") {
		return ".", nil
	}
	if !cleanRelative(name, false) {
		return "", errors.New("unsafe archive member path")
	}
	return name, nil
}

func (e *packageExtraction) count() error {
	e.entries++
	if e.entries > maxPackageEntries {
		return errors.New("package exceeds 10000 archive entries")
	}
	return nil
}

func (e *packageExtraction) file(name string, size int64, mode fs.FileMode, reader io.Reader) error {
	if size < 0 || size > maxPackageFile || e.total > maxPackageExpanded-size {
		return errors.New("unpacked package exceeds file or total size limit")
	}
	if _, exists := e.members[name]; exists {
		return errors.New("duplicate archive file")
	}
	e.total += size
	if err := e.root.MkdirAll(path.Dir(name), 0700); err != nil {
		return err
	}
	out, err := e.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(reader, size+1))
	closeErr := out.Close()
	if copyErr != nil {
		return errors.New("could not unpack package member")
	}
	if closeErr != nil {
		return closeErr
	}
	if n != size {
		return errors.New("archive member size mismatch")
	}
	e.members[name] = packageMember{digest: hex.EncodeToString(hash.Sum(nil)), mode: 0644 | (mode & 0111)}
	return nil
}

func (e *packageExtraction) directory(name string) error {
	return e.root.MkdirAll(name, 0700)
}

func (e *packageExtraction) zip(source *os.File, size int64) error {
	if err := packageZIPDirectoryLimit(source, size); err != nil {
		return err
	}
	archive, err := zip.NewReader(source, size)
	if err != nil {
		return errors.New("invalid ZIP package")
	}
	if len(archive.File) > maxPackageEntries {
		return errors.New("package exceeds 10000 archive entries")
	}
	for _, member := range archive.File {
		if err = e.count(); err != nil {
			return err
		}
		mode := member.Mode()
		if mode.Type() != 0 && mode.Type() != os.ModeDir {
			return errors.New("archive links and special entries are forbidden")
		}
		name, err := packageArchiveName(member.Name, mode.IsDir())
		if err != nil {
			return err
		}
		if mode.IsDir() {
			if member.UncompressedSize64 != 0 {
				return errors.New("archive directory has file content")
			}
			if err = e.directory(name); err != nil {
				return err
			}
			continue
		}
		if member.UncompressedSize64 > uint64(maxPackageFile) {
			return errors.New("unpacked package file exceeds 64 MiB")
		}
		reader, err := member.Open()
		if err != nil {
			return errors.New("invalid ZIP member")
		}
		err = e.file(name, int64(member.UncompressedSize64), mode, reader)
		reader.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *packageExtraction) tar(source *os.File) error {
	gz, err := gzip.NewReader(source)
	if err != nil {
		return errors.New("invalid gzip package")
	}
	defer gz.Close()
	// Bound headers, PAX records, padding and concatenated gzip data as well as
	// file payloads. This also forces gzip checksums to be read after tar EOF.
	expanded := &io.LimitedReader{R: gz, N: maxPackageExpanded + (16 << 20) + 1}
	archive := tar.NewReader(expanded)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("invalid tar package")
		}
		if err = e.count(); err != nil {
			return err
		}
		directory := header.Typeflag == tar.TypeDir
		if !directory && header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return errors.New("archive links and special entries are forbidden")
		}
		for key := range header.PAXRecords {
			if strings.HasPrefix(key, "GNU.sparse") {
				return errors.New("sparse archive entries are forbidden")
			}
		}
		name, err := packageArchiveName(header.Name, directory)
		if err != nil {
			return err
		}
		if directory {
			if header.Size != 0 {
				return errors.New("archive directory has file content")
			}
			if err = e.directory(name); err != nil {
				return err
			}
			continue
		}
		if err = e.file(name, header.Size, fs.FileMode(header.Mode), archive); err != nil {
			return err
		}
	}
	if _, err = io.Copy(io.Discard, expanded); err != nil {
		return errors.New("invalid gzip package checksum")
	}
	if expanded.N <= 0 {
		return errors.New("unpacked archive stream exceeds size limit")
	}
	return nil
}

func (e *packageExtraction) mapFiles(p Plugin) (map[string]string, error) {
	targets := map[string]string{}
	for _, mapping := range p.Mappings {
		found := false
		for source := range e.members {
			target := ""
			switch {
			case source == mapping.From:
				if mapping.To == "." {
					return nil, errors.New("a file mapping requires a destination filename")
				}
				target = mapping.To
			case mapping.From == ".":
				target = path.Join(mapping.To, source)
			case strings.HasPrefix(source, mapping.From+"/"):
				target = path.Join(mapping.To, strings.TrimPrefix(source, mapping.From+"/"))
			default:
				continue
			}
			found = true
			if !ValidTarget(target) {
				return nil, fmt.Errorf("unsafe mapped target: %s", target)
			}
			if _, exists := targets[target]; exists {
				return nil, fmt.Errorf("duplicate mapped target: %s", target)
			}
			targets[target] = source
		}
		if !found {
			return nil, fmt.Errorf("mapping source has no files: %s", mapping.From)
		}
	}
	for target := range targets {
		for parent := path.Dir(target); parent != "."; parent = path.Dir(parent) {
			if _, exists := targets[parent]; exists {
				return nil, fmt.Errorf("mapped file conflicts with directory: %s", parent)
			}
		}
	}
	if len(targets) == 0 {
		return nil, errors.New("package mappings select no files")
	}
	return targets, nil
}

// Directory entries must be durable as well as file contents before a package
// can be referenced by a receipt. Sync children before their parents.
func packageSyncDirectories(root *os.Root, files []string) error {
	directories := map[string]bool{".": true}
	for _, file := range files {
		for directory := path.Dir(file); directory != "."; directory = path.Dir(directory) {
			directories[directory] = true
		}
	}
	names := make([]string, 0, len(directories))
	for directory := range directories {
		names = append(names, directory)
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	for _, directory := range names {
		file, err := root.Open(directory)
		if err != nil {
			return err
		}
		err = file.Sync()
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// archive/zip allocates central-directory entries before callers can inspect
// their count. Scan bounded headers first so an archive cannot exhaust memory
// using comments, extra fields, or a dishonest entry count.
func packageZIPDirectoryLimit(source io.ReaderAt, size int64) error {
	const endSize = int64(22)
	if size < endSize {
		return errors.New("invalid ZIP package")
	}
	tailSize := min(size, endSize+65535)
	tail := make([]byte, tailSize)
	if _, err := source.ReadAt(tail, size-tailSize); err != nil {
		return errors.New("invalid ZIP directory")
	}
	end := -1
	for i := len(tail) - int(endSize); i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:i+4]) == 0x06054b50 && i+int(endSize)+int(binary.LittleEndian.Uint16(tail[i+20:i+22])) == len(tail) {
			end = i
			break
		}
	}
	if end < 0 {
		return errors.New("invalid ZIP directory")
	}
	record := tail[end:]
	disk := binary.LittleEndian.Uint16(record[4:6])
	directoryDisk := binary.LittleEndian.Uint16(record[6:8])
	diskCount := binary.LittleEndian.Uint16(record[8:10])
	count := binary.LittleEndian.Uint16(record[10:12])
	directorySize := binary.LittleEndian.Uint32(record[12:16])
	directoryOffset := binary.LittleEndian.Uint32(record[16:20])
	if disk != 0 || directoryDisk != 0 || diskCount != count || count == 0xffff || directorySize == 0xffffffff || directoryOffset == 0xffffffff {
		return errors.New("ZIP64 and multi-disk packages are unsupported")
	}
	if count > maxPackageEntries || directorySize > 8<<20 {
		return errors.New("ZIP metadata exceeds package limits")
	}
	limit := int64(directoryOffset) + int64(directorySize)
	if limit != size-tailSize+int64(end) {
		return errors.New("invalid ZIP directory layout")
	}
	var header [46]byte
	actual := 0
	for offset := int64(directoryOffset); offset < limit; {
		if limit-offset < int64(len(header)) {
			return errors.New("invalid ZIP directory entry")
		}
		if _, err := source.ReadAt(header[:], offset); err != nil {
			return errors.New("invalid ZIP directory entry")
		}
		if binary.LittleEndian.Uint32(header[0:4]) != 0x02014b50 || binary.LittleEndian.Uint16(header[34:36]) != 0 {
			return errors.New("invalid ZIP directory entry")
		}
		actual++
		if actual > maxPackageEntries {
			return errors.New("package exceeds 10000 archive entries")
		}
		length := int64(len(header)) + int64(binary.LittleEndian.Uint16(header[28:30])) + int64(binary.LittleEndian.Uint16(header[30:32])) + int64(binary.LittleEndian.Uint16(header[32:34]))
		if length > limit-offset {
			return errors.New("invalid ZIP directory entry")
		}
		offset += length
	}
	if actual != int(count) {
		return errors.New("invalid ZIP directory entry count")
	}
	return nil
}
