package objectstore

// Directories using Stow

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/graymeta/stow"
	"github.com/pkg/errors"
)

var _ Directory = (*directory)(nil)

type directory struct {
	// Every directory is part of a bucket.
	bucket *bucket
	path   string // Starts (and if needed, ends) with a '/'
}

// String creates a string representation that can used by OpenDirectory()
func (d *directory) String() string {
	return fmt.Sprintf("%s%s", d.bucket.hostEndPoint, d.path)
}

// CreateDirectory creates the d.path/dir/ object.
func (d *directory) CreateDirectory(ctx context.Context, dir string) (Directory, error) {
	dir = d.absDirName(dir)
	// Create directory marker
	if err := d.PutBytes(ctx, dir, nil, nil); err != nil {
		return nil, err
	}
	return &directory{
		bucket: d.bucket,
		path:   dir,
	}, nil
}

// GetDirectory gets the directory object
func (d *directory) GetDirectory(ctx context.Context, dir string) (Directory, error) {
	if dir == "" {
		return d, nil
	}
	dir = d.absDirName(dir)
	_, err := d.bucket.container.Item(cloudName(dir))
	if err != nil {
		return nil, errors.Wrapf(err, "could not get directory marker %s", dir)
	}
	return &directory{
		bucket: d.bucket,
		path:   dir,
	}, nil
}

// ListDirectories lists all the directories that have d.path as the prefix.
// the returned map is indexed by the relative directory name (without trailing '/')
func (d *directory) ListDirectories(ctx context.Context) (map[string]Directory, error) {
	if d.path == "" {
		return nil, errors.New("invalid entry")
	}

	directories := make(map[string]Directory, 0)

	err := stow.Walk(d.bucket.container, cloudName(d.path), 10000,
		func(item stow.Item, err error) error {
			if err != nil {
				return err
			}

			dir := strings.TrimPrefix(item.Name(), cloudName(d.path))
			if dir == "" {
				// e.g., /<d.path>/
				return nil
			}
			// Directories will end with '/' in their names
			if dirEnt, ok := isDirectoryObject(dir); ok {
				// Use maps to uniqify
				// e.g., /dir1/, /dir1/file1, /dir1/dir2/, /dir1/dir2/file2 will leave /dir
				directories[dirEnt] = &directory{
					bucket: d.bucket,
					path:   d.absDirName(dirEnt),
				}
			}

			return nil
		})
	if err != nil {
		return nil, err
	}
	return directories, nil
}

// ListObjects lists all the files that have d.dirname as the prefix.
func (d *directory) ListObjects(ctx context.Context) ([]string, error) {
	if d.path == "" {
		return nil, errors.New("invalid entry")
	}

	objects := make([]string, 0, 1)
	err := stow.Walk(d.bucket.container, cloudName(d.path), 10000,
		func(item stow.Item, err error) error {
			if err != nil {
				return err
			}
			objName := strings.TrimPrefix(item.Name(), cloudName(d.path))
			if objName != "" && strings.Index(objName, "/") == -1 {
				objects = append(objects, objName)
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return objects, nil
}

// DeleteDirectory deletes all objects that have d.path as the prefix
// <bucket>/<d.path/<everything> including <bucket>/<d.path>/<some dir>/<objects>
func (d *directory) DeleteDirectory(ctx context.Context) error {
	if d.path == "" {
		return errors.New("invalid entry")
	}

	// Walk to find all entries that match the d.path prefix.
	err := stow.Walk(d.bucket.container, cloudName(d.path), 10000,
		func(item stow.Item, err error) error {
			if err != nil {
				return err
			}

			return d.bucket.container.RemoveItem(item.Name())
		})

	if err != nil {
		return err
	}

	return nil
}

func (d *directory) Get(ctx context.Context, name string) (io.ReadCloser, map[string]string, error) {
	if d.path == "" {
		return nil, nil, errors.New("invalid entry")
	}

	objName := d.absPathName(name)

	item, err := d.bucket.container.Item(cloudName(objName))
	if err != nil {
		return nil, nil, err
	}

	// Open the object and read all data
	r, err := item.Open()
	if err != nil {
		return nil, nil, err
	}
	rTags, err := item.Metadata()
	if err != nil {
		return nil, nil, err
	}

	// Convert tags:map[string]interface{} into map[string]string
	tags := make(map[string]string)
	for key, val := range rTags {
		if sVal, ok := val.(string); ok {
			tags[key] = sVal
		}
	}

	return r, tags, nil
}

// Get data and tags associated with an object <bucket>/<d.path>/name.
func (d *directory) GetBytes(ctx context.Context, name string) ([]byte, map[string]string, error) {
	r, tags, err := d.Get(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()

	data, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}

	return data, tags, nil
}

func (d *directory) Put(ctx context.Context, name string, r io.Reader, size int64, tags map[string]string) error {
	if d.path == "" {
		return errors.New("invalid entry")
	}
	// K10 tags include '/'. Remove them, at least for S3
	sTags := sanitizeTags(tags)

	objName := d.absPathName(name)

	// For versioned buckets, Put can return the new version name
	// TODO: Support versioned buckets
	_, err := d.bucket.container.Put(cloudName(objName), r, size, sTags)
	return err
}

// Put stores a blob in d.path/<name>
func (d *directory) PutBytes(ctx context.Context, name string, data []byte, tags map[string]string) error {
	return d.Put(ctx, name, bytes.NewReader(data), int64(len(data)), tags)
}

// Delete removes an object
func (d *directory) Delete(ctx context.Context, name string) error {
	if d.path == "" {
		return errors.New("invalid entry")
	}

	objName := d.absPathName(name)

	return d.bucket.container.RemoveItem(cloudName(objName))
}

// If name does not start with '/', prefix with d.path. Add '/' as suffix
func (d *directory) absDirName(dir string) string {
	dir = d.absPathName(dir)

	// End with a '/'
	if !strings.HasSuffix(dir, "/") {
		dir = filepath.Clean(dir) + "/"
	}

	return dir
}

// S3 ignores the root '/' while creating objects. During
// filtering operations however, the '/' is not ignored.
// GCS creates an explicit '/' in the bucket. cloudName
// strips the initial '/' for stow operations. '/' still
// implies root for objectstore.
func cloudName(dir string) string {
	return strings.TrimPrefix(dir, "/")
}

// If name does not start with '/', prefix with d.path.
func (d *directory) absPathName(name string) string {
	if name == "" {
		return ""
	}
	if !filepath.IsAbs(name) {
		name = d.path + name
	}

	return name
}

// sanitizeTags replaces '/' with "-" in tag keys
func sanitizeTags(tags map[string]string) map[string]interface{} {
	cTags := make(map[string]interface{})
	for key, val := range tags {
		cKey := strings.Replace(key, "/", "-", -1)
		cTags[cKey] = val
	}
	return cTags
}

// isDirectoryObject checks if path includes one '/'.
// If so, returns value until first '/'
// path is of the form elem1/elem2/, returns elem1
func isDirectoryObject(path string) (string, bool) {
	s := strings.SplitN(path, "/", 3)
	switch len(s) {
	case 1:
		// No '/'
		return "", false
	case 2:
		return s[0], true
	case 3:
		// dir1/dir2/elem will return false
		return "", false
	}

	// Not reached
	return "", false
}
