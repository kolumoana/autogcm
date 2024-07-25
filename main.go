	"path/filepath"
	"github.com/pmezard/go-difflib/difflib"
const maxFileDiffSize = 10000     // Maximum characters for each file's diff
const maxAddedFilePreview = 10000 // Maximum characters for previewing added files

	fmt.Fprint(os.Stdout, commitMessage)
var excludedExtensions = map[string]bool{
	".pdf":   true,
	".jpg":   true,
	".jpeg":  true,
	".png":   true,
	".gif":   true,
	".zip":   true,
	".tar":   true,
	".gz":    true,
	".exe":   true,
	".dll":   true,
	".so":    true,
	".dylib": true,
	".class": true,
	".pyc":   true,
	".jar":   true,
	".war":   true,
	".ear":   true,
	".sum":   true,
}


		if g.shouldExcludeFile(filePath) {
			diff.WriteString(fmt.Sprintf("Excluded file: %s (binary or large data file)\n", filePath))
			continue
		}

		case git.Added:
			patch, err = g.getAddedPatch(filePath, maxAddedFilePreview)
		case git.Modified:
			patch, err = g.getModifiedPatch(filePath)

		// Truncate the patch if it exceeds the max size (except for added files)
		if fileStatus.Staging != git.Added && len(patch) > maxFileDiffSize {
			patch, truncated := g.truncatePatch(patch, maxFileDiffSize)
			if truncated {
				patch += fmt.Sprintf("\n... (truncated, total %d characters) ...\n", len(patch))
			}
		}

func (g *CommitMessageGenerator) shouldExcludeFile(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	if excludedExtensions[ext] {
		return true
	}

	// Check if the file is likely to be a binary file
	content, err := g.getUnstagedFileContent(filePath)
	if err != nil {
		// If we can't read the file, assume it's binary
		return true
	}

	// Check for null bytes, which are common in binary files
	if bytes.IndexByte([]byte(content), 0) != -1 {
		return true
	}

	return false
}

func (g *CommitMessageGenerator) getAddedPatch(filePath string, maxPreview int) (string, error) {
	content, err := g.getUnstagedFileContent(filePath)
	if err != nil {
		return "", fmt.Errorf("getting file content: %w", err)
	}

	var diff bytes.Buffer
	diff.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", filePath, filePath))
	diff.WriteString("new file mode 100644\n")
	diff.WriteString("--- /dev/null\n")
	diff.WriteString(fmt.Sprintf("+++ b/%s\n", filePath))

	if len(content) > maxPreview {
		preview := content[:maxPreview]
		lineCount := strings.Count(content, "\n") + 1
		diff.WriteString(fmt.Sprintf("@@ -0,0 +1,%d @@ (preview)\n", lineCount))
		for _, line := range strings.Split(preview, "\n") {
			diff.WriteString("+" + line + "\n")
		}
		diff.WriteString(fmt.Sprintf("\n... (file truncated, total %d characters) ...\n", len(content)))
	} else {
		lineCount := strings.Count(content, "\n") + 1
		diff.WriteString(fmt.Sprintf("@@ -0,0 +1,%d @@\n", lineCount))
		for _, line := range strings.Split(content, "\n") {
			diff.WriteString("+" + line + "\n")
		}
	}

	return diff.String(), nil
}

func (g *CommitMessageGenerator) getModifiedPatch(filePath string) (string, error) {
	// Get the staged version of the file
	stagedContent, err := g.getStagedFileContent(filePath)
	// Get the unstaged version of the file
	unstagedContent, err := g.getUnstagedFileContent(filePath)
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(stagedContent),
		B:        difflib.SplitLines(unstagedContent),
		FromFile: "a/" + filePath,
		ToFile:   "b/" + filePath,
		Context:  3,
	})
	if err != nil {
		return "", fmt.Errorf("generating diff: %w", err)
	}

	return fmt.Sprintf("diff --git a/%s b/%s\n%s", filePath, filePath, diff), nil
}

func (g *CommitMessageGenerator) truncatePatch(patch string, maxSize int) (string, bool) {
	if len(patch) <= maxSize {
		return patch, false
	}

	lines := strings.Split(patch, "\n")
	var truncated bytes.Buffer
	var currentSize int

	// Always include the file name and diff header
	for i, line := range lines {
		if i < 2 || strings.HasPrefix(line, "@@") {
			truncated.WriteString(line + "\n")
			currentSize += len(line) + 1
			continue
		}

		if currentSize+len(line)+1 > maxSize {
			break
		}

		truncated.WriteString(line + "\n")
		currentSize += len(line) + 1
	}

	return truncated.String(), true