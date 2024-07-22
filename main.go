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
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: OPENAI_API_KEY environment variable is not set.")
		os.Exit(1)
	}

	diff, err := getStagedDiff()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if diff == "" {
		fmt.Fprintln(os.Stderr, "No staged changes found.")
		os.Exit(1)
	}

	commitMessage, err := generateCommitMessage(apiKey, diff)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating commit message: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(commitMessage)
}

func getStagedDiff() (string, error) {
	repo, err := git.PlainOpen(".")
	if err != nil {
		return "", fmt.Errorf("opening repository: %w", err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("getting worktree: %w", err)
	}

	status, err := worktree.Status()
	if err != nil {
		return "", fmt.Errorf("getting status: %w", err)
	}

	var diff bytes.Buffer
	for filePath, fileStatus := range status {
		if fileStatus.Staging == git.Added || fileStatus.Staging == git.Modified {
			staged, err := getStagedFileContent(repo, filePath)
			if err != nil {
				return "", fmt.Errorf("getting staged content for %s: %w", filePath, err)
			}

			unstaged, err := getUnstagedFileContent(worktree, filePath)
			if err != nil {
				return "", fmt.Errorf("getting unstaged content for %s: %w", filePath, err)
			}

			patch, err := generateUnifiedDiff(filePath, staged, unstaged)
			if err != nil {
				return "", fmt.Errorf("generating patch for %s: %w", filePath, err)
			}

			diff.WriteString(patch)
		}
	}

	return diff.String(), nil
}

func getStagedFileContent(repo *git.Repository, filePath string) (string, error) {
	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("getting HEAD: %w", err)
	}

	commit, err := repo.CommitObject(head.Hash())
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

	content, err := file.Contents()
	if err != nil {
		return "", fmt.Errorf("reading file contents: %w", err)
	}

	return content, nil
}

func getUnstagedFileContent(worktree *git.Worktree, filePath string) (string, error) {
	file, err := worktree.Filesystem.Open(filePath)
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

func generateUnifiedDiff(filePath, oldContent, newContent string) (string, error) {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	var diff bytes.Buffer
	diff.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", filePath, filePath))
	diff.WriteString("--- a/" + filePath + "\n")
	diff.WriteString("+++ b/" + filePath + "\n")

	chunks := diffLines(oldLines, newLines)
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

func diffLines(oldLines, newLines []string) []diffChunk {
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

func generateCommitMessage(apiKey, diff string) (string, error) {
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
