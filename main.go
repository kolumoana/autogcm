package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

//go:embed systemPrompt.md
var systemPrompt string

type CommitMessageGenerator struct {
	repo     *git.Repository
	worktree *git.Worktree
	apiKey   string
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

	commitMessage, err := generator.generateCommitMessage(diff)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating commit message: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(commitMessage)
}

func NewCommitMessageGenerator() (*CommitMessageGenerator, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
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
		repo:     repo,
		worktree: worktree,
		apiKey:   apiKey,
	}, nil
}

func (g *CommitMessageGenerator) getStagedDiff() (string, error) {
	status, err := g.worktree.Status()
	if err != nil {
		return "", fmt.Errorf("getting status: %w", err)
	}

	var diff bytes.Buffer
	for filePath, fileStatus := range status {
		var patch string
		var err error

		switch fileStatus.Staging {
		case git.Added, git.Modified:
			patch, err = g.getAddedOrModifiedPatch(filePath)
		case git.Deleted:
			patch, err = g.getDeletedPatch(filePath)
		default:
			continue
		}

		if err != nil {
			return "", fmt.Errorf("generating patch for %s: %w", filePath, err)
		}
		diff.WriteString(patch)
	}

	return diff.String(), nil
}

func (g *CommitMessageGenerator) getAddedOrModifiedPatch(filePath string) (string, error) {
	staged, err := g.getStagedFileContent(filePath)
	if err != nil {
		return "", fmt.Errorf("getting staged content: %w", err)
	}

	unstaged, err := g.getUnstagedFileContent(filePath)
	if err != nil {
		return "", fmt.Errorf("getting unstaged content: %w", err)
	}

	return g.generateUnifiedDiff(filePath, staged, unstaged)
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

func (g *CommitMessageGenerator) generateUnifiedDiff(filePath, oldContent, newContent string) (string, error) {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	var diff bytes.Buffer
	diff.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", filePath, filePath))
	diff.WriteString("--- a/" + filePath + "\n")
	diff.WriteString("+++ b/" + filePath + "\n")

	chunks := g.diffLines(oldLines, newLines)
	for _, chunk := range chunks {
		diff.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", chunk.oldStart, chunk.oldLines, chunk.newStart, chunk.newLines))
		for _, line := range chunk.lines {
			diff.WriteString(line + "\n")
		}
	}

	return diff.String(), nil
}

type diffChunk struct {
	oldStart, oldLines, newStart, newLines int
	lines                                  []string
}

func (g *CommitMessageGenerator) diffLines(oldLines, newLines []string) []diffChunk {
	var chunks []diffChunk
	oldIndex, newIndex := 0, 0

	for oldIndex < len(oldLines) || newIndex < len(newLines) {
		chunk := diffChunk{oldStart: oldIndex + 1, newStart: newIndex + 1}

		for oldIndex < len(oldLines) && newIndex < len(newLines) && oldLines[oldIndex] == newLines[newIndex] {
			chunk.lines = append(chunk.lines, " "+oldLines[oldIndex])
			oldIndex++
			newIndex++
		}

		oldStart := oldIndex
		for oldIndex < len(oldLines) && (newIndex >= len(newLines) || oldLines[oldIndex] != newLines[newIndex]) {
			chunk.lines = append(chunk.lines, "-"+oldLines[oldIndex])
			oldIndex++
		}

		newStart := newIndex
		for newIndex < len(newLines) && (oldIndex >= len(oldLines) || oldLines[oldIndex] != newLines[newIndex]) {
			chunk.lines = append(chunk.lines, "+"+newLines[newIndex])
			newIndex++
		}

		if len(chunk.lines) > 0 {
			chunk.oldLines = oldIndex - oldStart
			chunk.newLines = newIndex - newStart
			chunks = append(chunks, chunk)
		}
	}

	return chunks
}

func (g *CommitMessageGenerator) generateCommitMessage(diff string) (string, error) {
	url := "https://api.openai.com/v1/chat/completions"
	requestBody := OpenAIRequest{
		Model: "gpt-4o-mini",
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
	req.Header.Set("Authorization", "Bearer "+g.apiKey)

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
