package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/pmezard/go-difflib/difflib"
)

const maxFileDiffSize = 8000     // Maximum characters for each file's diff
const maxAddedFilePreview = 5000 // Maximum characters for previewing added files

//go:embed systemPrompt.md
var systemPrompt string

type CommitMessageGenerator struct {
	repo         *git.Repository
	worktree     *git.Worktree
	groqAPIKey   string
	openAIAPIKey string
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OpenAIRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

type OpenAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func main() {
	generator, err := NewCommitMessageGenerator()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	diff, err := generator.getStagedDiff()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if diff == "" {
		fmt.Fprintln(os.Stderr, "No staged changes found.")
		os.Exit(1)
	}

	commitMessage, err := generator.lazyGenerateCommitMessage(diff)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating commit message: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprint(os.Stdout, commitMessage)
}

func NewCommitMessageGenerator() (*CommitMessageGenerator, error) {
	groqAPIKey := os.Getenv("GROQ_API_KEY")
	if groqAPIKey == "" {
		return nil, fmt.Errorf("GROQ_API_KEY environment variable is not set")
	}
	openAIAPIKey := os.Getenv("OPENAI_API_KEY")
	if openAIAPIKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY environment variable is not set")
	}

	repo, err := git.PlainOpen(".")
	if err != nil {
		return nil, fmt.Errorf("opening repository: %w", err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("getting worktree: %w", err)
	}

	return &CommitMessageGenerator{
		repo:         repo,
		worktree:     worktree,
		groqAPIKey:   groqAPIKey,
		openAIAPIKey: openAIAPIKey,
	}, nil
}

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

func (g *CommitMessageGenerator) getStagedDiff() (string, error) {
	status, err := g.worktree.Status()
	if err != nil {
		return "", fmt.Errorf("getting status: %w", err)
	}

	var diff bytes.Buffer

	for filePath, fileStatus := range status {
		if g.shouldExcludeFile(filePath) {
			diff.WriteString(fmt.Sprintf("Excluded file: %s (binary or large data file)\n", filePath))
			continue
		}

		var patch string
		var err error

		switch fileStatus.Staging {
		case git.Added:
			patch, err = g.getAddedPatch(filePath, maxAddedFilePreview)
		case git.Modified:
			patch, err = g.getModifiedPatch(filePath)
		case git.Deleted:
			patch, err = g.getDeletedPatch(filePath)
		default:
			continue
		}

		if err != nil {
			return "", fmt.Errorf("generating patch for %s: %w", filePath, err)
		}

		// Truncate the patch if it exceeds the max size (except for added files)
		if fileStatus.Staging != git.Added && len(patch) > maxFileDiffSize {
			patch, truncated := g.truncatePatch(patch, maxFileDiffSize)
			if truncated {
				patch += fmt.Sprintf("\n... (truncated, total %d characters) ...\n", len(patch))
			}
		}

		diff.WriteString(patch)
	}

	return diff.String(), nil
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
	if err != nil {
		return "", fmt.Errorf("getting staged content: %w", err)
	}

	// Get the unstaged version of the file
	unstagedContent, err := g.getUnstagedFileContent(filePath)
	if err != nil {
		return "", fmt.Errorf("getting unstaged content: %w", err)
	}

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
}

func (g *CommitMessageGenerator) getDeletedPatch(filePath string) (string, error) {
	content, err := g.getStagedFileContent(filePath)
	if err != nil {
		return "", fmt.Errorf("getting file content: %w", err)
	}

	var diff bytes.Buffer
	diff.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", filePath, filePath))
	diff.WriteString("deleted file mode 100644\n")
	diff.WriteString("--- a/" + filePath + "\n")
	diff.WriteString("+++ /dev/null\n")

	lines := strings.Split(content, "\n")
	diff.WriteString(fmt.Sprintf("@@ -1,%d +0,0 @@\n", len(lines)))
	for _, line := range lines {
		diff.WriteString("-" + line + "\n")
	}

	return diff.String(), nil
}

func (g *CommitMessageGenerator) getStagedFileContent(filePath string) (string, error) {
	head, err := g.repo.Head()
	if err != nil {
		return "", fmt.Errorf("getting HEAD: %w", err)
	}

	commit, err := g.repo.CommitObject(head.Hash())
	if err != nil {
		return "", fmt.Errorf("getting commit object: %w", err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return "", fmt.Errorf("getting tree: %w", err)
	}

	file, err := tree.File(filePath)
	if err != nil {
		if err == object.ErrFileNotFound {
			return "", nil // 新規ファイルの場合は空文字列を返す
		}
		return "", fmt.Errorf("getting file from tree: %w", err)
	}

	return file.Contents()
}

func (g *CommitMessageGenerator) getUnstagedFileContent(filePath string) (string, error) {
	file, err := g.worktree.Filesystem.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("opening file: %w", err)
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("reading file contents: %w", err)
	}

	return string(content), nil
}

func (g *CommitMessageGenerator) lazyGenerateCommitMessage(diff string) (string, error) {
	groqUrl, groqModel, groqAPIKey := "https://api.groq.com/openai/v1/chat/completions", "llama3-70b-8192", g.groqAPIKey
	openAIUrl, openAIModel, openAIAPIKey := "https://api.openai.com/v1/chat/completions", "gpt-4o-mini-2024-07-18", g.openAIAPIKey

	groqResp, err := g.generateCommitMessage(groqUrl, groqModel, diff, groqAPIKey)
	if err != nil {
		return g.generateCommitMessage(openAIUrl, openAIModel, diff, openAIAPIKey)
	}

	return groqResp, nil
}

func (g *CommitMessageGenerator) generateCommitMessage(
	url string,
	model string,
	diff string,
	apiKey string,
) (string, error) {
	fmt.Println("url", url)
	fmt.Println("model", model)
	fmt.Println("diff", diff)
	fmt.Println("apiKey", apiKey)
	requestBody := OpenAIRequest{
		Model: model,
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: diff},
		},
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("marshaling request body: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response body: %w", err)
	}

	var openAIResp OpenAIResponse
	err = json.Unmarshal(body, &openAIResp)
	if err != nil {
		return "", fmt.Errorf("unmarshaling response: %w", err)
	}

	if len(openAIResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response. Full response: %s", string(body))
	}

	commitMessage := openAIResp.Choices[0].Message.Content
	commitMessage = strings.TrimSpace(commitMessage)
	commitMessage = strings.TrimPrefix(commitMessage, "```")
	commitMessage = strings.TrimSuffix(commitMessage, "```")
	commitMessage = strings.TrimSpace(commitMessage)

	return commitMessage, nil
}
