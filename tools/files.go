//DESC: contains tools for navigating and getting file info

package tools

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	ignore "github.com/sabhiram/go-gitignore"
)

var ProjectRoot string

func is_sub_dir(dir string) bool {
	if ProjectRoot == ""{
		return false
	}

	rootAbs , err := filepath.Abs(ProjectRoot)
	
	if err != nil {
		return false
	}

	targetAbs , err := filepath.Abs(dir)

	if err != nil {
		return false
	}
	
	rel , err := filepath.Rel(rootAbs , targetAbs)

	if err != nil {
		return false
	}

	return !strings.HasPrefix(rel,"..") && rel != ".."
}

func load_ignores(root string) []*ignore.GitIgnore {
	var ignores []*ignore.GitIgnore

	gitignore, err := ignore.CompileIgnoreFile(filepath.Join(root, ".gitignore"))
	if err == nil {
		ignores = append(ignores, gitignore)
	}

	agentignore, err := ignore.CompileIgnoreFile(filepath.Join(root, ".agentignore"))
	if err == nil {
		ignores = append(ignores, agentignore)
	}

	return ignores
}

func is_ignored(ignores []*ignore.GitIgnore, rel string) bool {
	for _, ig := range ignores {
		if ig.MatchesPath(rel) {
			return true
		}
	}
	return false
}

func list_dir(root, path string, depth int, ignores []*ignore.GitIgnore) string {
	contents, err := os.ReadDir(path)
    
	if err != nil {
        return err.Error()
    }

    var resp string
    indent := strings.Repeat("  ", depth)

    for _, content := range contents {
        // get path relative to root for gitignore matching
        rel, _ := filepath.Rel(root, filepath.Join(path, content.Name()))

        if is_ignored(ignores , rel) {
            continue
        }

        if content.Type().IsDir() {
            resp += indent + content.Name() + "/\n"
            resp += list_dir(root, filepath.Join(path, content.Name()), depth+1, ignores)
        } else {
            resp += indent + content.Name() + "\n"
        }
    }
    return resp
}

// basicaly a recursive ls
func List_directory(path string) string {
    
	//Guard so that the ai cant ever access any dir outside of the working one
	if !is_sub_dir(path){
		return "error : path is outside of the projects root"
	}

	abs_path, err := filepath.Abs(path)
    if err != nil {
        return err.Error()
    }

    // load .gitignore from the root if it exists
	ignores := load_ignores(abs_path)

    return list_dir(abs_path, abs_path, 0, ignores)
}

//create a file with a Name
func Create_file(path string, filename string, content string) string {
	fullPath := filepath.Join(ProjectRoot, path, filename)
	if !is_sub_dir(fullPath) {
		return "error: cannot create file, path is outside of the projects root"
	}
	//Create any missing directories
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return "error creating directories: " + err.Error()
	}
	err := os.WriteFile(fullPath, []byte(content), 0644)
	if err != nil {
		return "error writing file: " + err.Error()
	}
	return "successfully created file at " + fullPath
}

//delets a file and if the parent dir is empty (imideate parent only) it deletes the dir too
func Delete_file(path string , filename string) string {
	fullPath := filepath.Join(ProjectRoot, path, filename)
	
	if !is_sub_dir(fullPath) {
		return "error: cannot delete file outside of working dir"
	}

	if err := os.Remove(filepath.Join(fullPath)); err != nil {
		return "error deleting file: "+ err.Error()
	}
	
	dir := filepath.Dir(fullPath)
	extra := ""
	
	rootAbs , err := filepath.Abs(ProjectRoot)

	if err == nil {
		if dirAbs, err := filepath.Abs(dir); err == nil && dirAbs != rootAbs {
			if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
				os.Remove(dir)
				extra = " also deleted directory "+dir
			}
		}
	}

	return "successfully deleted the file"+extra
}

// Get_files_info lists the immediate (non-recursive) contents of a directory,
// including size/type, while respecting .gitignore / .agentignore.
func Get_files_info(path string) string {
	fullPath := filepath.Join(ProjectRoot, path)
	if !is_sub_dir(fullPath) {
		return "error: cannot access path outside of the projects root"
	}
 
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return "error reading directory: " + err.Error()
	}
 
	ignores := load_ignores(ProjectRoot)
 
	var resp string
	for _, entry := range entries {
		rel, err := filepath.Rel(ProjectRoot, filepath.Join(fullPath, entry.Name()))
		if err != nil {
			continue
		}
		if is_ignored(ignores, rel) {
			continue
		}
 
		info, err := entry.Info()
		if err != nil {
			continue
		}
 
		if entry.IsDir() {
			resp += entry.Name() + "/: dir\n"
		} else {
			resp += entry.Name() + ": file, " + strconv.FormatInt(info.Size() , 10) +" bytes\n"
		}
	}
 
	if resp == "" {
		return "directory is empty (or all contents are ignored)"
	}
	return resp
}
