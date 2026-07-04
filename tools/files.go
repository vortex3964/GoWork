//DESC: contains tools for navigating and getting file info

package tools

import (
	"os"
	"path/filepath"
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
