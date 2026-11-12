package server

import (
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var errWorkspaceManaged = errors.New("系统数据或链接路径不可修改，请使用对应的任务或数据管理入口")
var workspaceManagedNames = []string{"traffic", "evidence", "archives", "transcripts", "sessions", "nuclei-templates", "config.json", "jwt.key"}

func workspaceManagedPath(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	first := parts[0]
	for _, name := range workspaceManagedNames {
		if first == name || runtime.GOOS == "windows" && strings.EqualFold(first, name) {
			return true
		}
	}
	return false
}

func workspaceManagedFiles(root *os.Root) []os.FileInfo {
	var out []os.FileInfo
	for _, name := range workspaceManagedNames {
		if info, err := root.Stat(name); err == nil {
			out = append(out, info)
		}
	}
	return out
}
func workspaceSameManaged(info os.FileInfo, managed []os.FileInfo) bool {
	for _, other := range managed {
		if os.SameFile(info, other) {
			return true
		}
	}
	return false
}
func workspaceManagedInfo(root *os.Root, info os.FileInfo) bool {
	return workspaceSameManaged(info, workspaceManagedFiles(root))
}

func workspacePlainName(name string) bool {
	if name == "" || name == "." || name == ".." || !filepath.IsLocal(name) || filepath.Base(name) != name {
		return false
	}
	return runtime.GOOS != "windows" || (!strings.ContainsAny(name, ":") && strings.TrimRight(name, " .") == name)
}

// Each ancestor is opened as a confined capability. Comparing the open handle
// with the preceding lstat rejects a link swapped in between check and open.
func workspaceParent(root *os.Root, rel string, create bool) (*os.Root, string, error) {
	if workspaceManagedPath(rel) {
		return nil, "", errWorkspaceManaged
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, part := range parts {
		if !workspacePlainName(part) {
			return nil, "", errWorkspaceManaged
		}
	}
	current, err := root.OpenRoot(".")
	if err != nil {
		return nil, "", err
	}
	for _, part := range parts[:len(parts)-1] {
		before, err := current.Lstat(part)
		if os.IsNotExist(err) && create {
			if err = current.Mkdir(part, 0755); err != nil && !os.IsExist(err) {
				current.Close()
				return nil, "", err
			}
			before, err = current.Lstat(part)
		}
		if err != nil {
			current.Close()
			return nil, "", err
		}
		if !before.IsDir() || before.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || workspaceManagedInfo(root, before) {
			current.Close()
			return nil, "", errWorkspaceManaged
		}
		child, err := current.OpenRoot(part)
		if err != nil {
			current.Close()
			return nil, "", err
		}
		after, err := child.Stat(".")
		if err != nil || !os.SameFile(before, after) {
			child.Close()
			current.Close()
			return nil, "", errWorkspaceManaged
		}
		current.Close()
		current = child
	}
	base := parts[len(parts)-1]
	if info, err := current.Lstat(base); err == nil {
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || workspaceManagedInfo(root, info) {
			current.Close()
			return nil, "", errWorkspaceManaged
		}
	} else if !os.IsNotExist(err) {
		current.Close()
		return nil, "", err
	}
	return current, base, nil
}

func workspaceAtomicWrite(parent *os.Root, name string, src io.Reader) error {
	tmp := ".workspace-" + rand.Text()
	file, err := parent.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	defer parent.Remove(tmp)
	_, writeErr := io.Copy(file, src)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	// Replace the directory entry, never truncate an existing hard-linked inode.
	return parent.Rename(tmp, name)
}

func workspaceReadOnly(root *os.Root, rel string) bool {
	if rel == "." {
		return false
	}
	parent, _, err := workspaceParent(root, rel, false)
	if parent != nil {
		parent.Close()
	}
	return err != nil
}

func workspaceMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, errWorkspaceManaged) {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	writeErr(w, http.StatusBadRequest, "无法修改路径："+err.Error())
}

func workspaceDeletionProtected(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return strings.EqualFold(parts[0], "tasks") && len(parts) <= 2
}
