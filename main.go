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

const maxFileDiffSize = 4000     // Maximum characters for each file's diff
const maxAddedFilePreview = 4000 // Maximum characters for previewing added files

//go:embed systemPrompt.md
var systemPrompt string

type CommitMessageGenerator struct {
	repo         *git.Repository
	worktree     *git.Worktree
	groqAPIKey   string
	openAIAPIKey string
	geminiAPIKey string
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

type GeminiRequest struct {
	Contents []GeminiContent `json:"contents"`
}

type GeminiContent struct {
	Parts []GeminiPart `json:"parts"`
}

type GeminiPart struct {
	Text string `json:"text"`
}

type GeminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
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
	geminiAPIKey := os.Getenv("GEMINI_API_KEY")
	groqAPIKey := os.Getenv("GROQ_API_KEY")
	openAIAPIKey := os.Getenv("OPENAI_API_KEY")
	
	// At least one API key must be set
	if geminiAPIKey == "" && groqAPIKey == "" && openAIAPIKey == "" {
		return nil, fmt.Errorf("At least one of GEMINI_API_KEY, GROQ_API_KEY, or OPENAI_API_KEY environment variable must be set")
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
		geminiAPIKey: geminiAPIKey,
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

func (g *CommitMessageGenerator) getRecentCommitMessages(limit int) ([]string, error) {
	iter, err := g.repo.Log(&git.LogOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting commit log: %w", err)
	}
	defer iter.Close()

	var messages []string
	count := 0
	err = iter.ForEach(func(c *object.Commit) error {
		if count >= limit {
			return fmt.Errorf("limit reached")
		}
		messages = append(messages, c.Message)
		count++
		return nil
	})

	if err != nil && err.Error() != "limit reached" {
		return nil, err
	}

	return messages, nil
}

func (g *CommitMessageGenerator) analyzeCommitStyle(messages []string) string {
	if len(messages) == 0 {
		return ""
	}

	var analysis bytes.Buffer
	analysis.WriteString("Recent commit messages for style reference:\n\n")
	for i, msg := range messages {
		if i >= 3 { // Show only first 3 full messages
			break
		}
		msg = strings.TrimSpace(msg)
		// Remove generated by lines
		lines := strings.Split(msg, "\n")
		var cleanedLines []string
		for _, line := range lines {
			if !strings.Contains(line, "Generated with") && !strings.Contains(line, "Co-Authored-By") {
				cleanedLines = append(cleanedLines, line)
			}
		}
		analysis.WriteString(fmt.Sprintf("Example %d:\n%s\n\n", i+1, strings.Join(cleanedLines, "\n")))
	}

	return analysis.String()
}

func (g *CommitMessageGenerator) lazyGenerateCommitMessage(diff string) (string, error) {
	// Get recent commit messages for style analysis
	recentMessages, err := g.getRecentCommitMessages(10)
	if err != nil {
		// If we can't get recent messages, proceed without them
		recentMessages = []string{}
	}

	commitStyle := g.analyzeCommitStyle(recentMessages)

	// Try Gemini first if API key is available
	if g.geminiAPIKey != "" {
		geminiResp, err := g.generateGeminiCommitMessage(diff, commitStyle)
		if err == nil {
			return geminiResp, nil
		}
		// If Gemini fails, continue to other APIs
	}

	// Try Groq if API key is available
	if g.groqAPIKey != "" {
		groqUrl := "https://api.groq.com/openai/v1/chat/completions"
		// コンテキスト長に基づいてモデルを選択（llama3-70b-8192のトークン制限8192、1トークン≈4文字）
		const llama3ContextLimit = 8192 * 4 // 32768文字
		var groqModel string
		if len(diff) > llama3ContextLimit {
			groqModel = "mixtral-8x7b-32768" // 長いdiffの場合
		} else {
			groqModel = "llama3-70b-8192" // 短いdiffの場合
		}

		groqResp, err := g.generateCommitMessage(groqUrl, groqModel, diff, g.groqAPIKey, commitStyle)
		if err == nil {
			return groqResp, nil
		}
		// If Groq fails, continue to OpenAI
	}

	// Try OpenAI as last fallback if API key is available
	if g.openAIAPIKey != "" {
		openAIUrl := "https://api.openai.com/v1/chat/completions"
		openAIModel := "gpt-4o-mini-2024-07-18"
		return g.generateCommitMessage(openAIUrl, openAIModel, diff, g.openAIAPIKey, commitStyle)
	}

	return "", fmt.Errorf("all available APIs failed to generate commit message")
}

func (g *CommitMessageGenerator) generateCommitMessage(
	url string,
	model string,
	diff string,
	apiKey string,
	commitStyle string,
) (string, error) {
	var userContent string
	if commitStyle != "" {
		userContent = fmt.Sprintf("%s\n\n%s", commitStyle, diff)
	} else {
		userContent = diff
	}

	requestBody := OpenAIRequest{
		Model: model,
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userContent},
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
	
	// Remove common prefixes that models might add
	prefixesToRemove := []string{
		"Here is the generated commit message:",
		"Here is the generated commit message:\n",
		"以下がコミットメッセージです:",
		"以下がコミットメッセージです:\n",
		"Generated commit message:",
		"Generated commit message:\n",
	}
	
	for _, prefix := range prefixesToRemove {
		commitMessage = strings.TrimPrefix(commitMessage, prefix)
	}
	
	commitMessage = strings.TrimSpace(commitMessage)
	commitMessage = strings.TrimPrefix(commitMessage, "```")
	commitMessage = strings.TrimSuffix(commitMessage, "```")
	commitMessage = strings.TrimSpace(commitMessage)

	return commitMessage, nil
}

func (g *CommitMessageGenerator) generateGeminiCommitMessage(diff string, commitStyle string) (string, error) {
	geminiUrl := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-1.5-flash:generateContent?key=%s", g.geminiAPIKey)
	
	var userContent string
	if commitStyle != "" {
		userContent = fmt.Sprintf("%s\n\n%s", commitStyle, diff)
	} else {
		userContent = diff
	}

	// Combine system prompt and user content for Gemini
	fullPrompt := fmt.Sprintf("%s\n\n%s", systemPrompt, userContent)
	
	requestBody := GeminiRequest{
		Contents: []GeminiContent{
			{
				Parts: []GeminiPart{
					{Text: fullPrompt},
				},
			},
		},
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("marshaling request body: %w", err)
	}

	req, err := http.NewRequest("POST", geminiUrl, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

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

	var geminiResp GeminiResponse
	err = json.Unmarshal(body, &geminiResp)
	if err != nil {
		return "", fmt.Errorf("unmarshaling response: %w", err)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content in response. Full response: %s", string(body))
	}

	commitMessage := geminiResp.Candidates[0].Content.Parts[0].Text
	commitMessage = strings.TrimSpace(commitMessage)
	
	// Remove common prefixes that models might add
	prefixesToRemove := []string{
		"Here is the generated commit message:",
		"Here is the generated commit message:\n",
		"以下がコミットメッセージです:",
		"以下がコミットメッセージです:\n",
		"Generated commit message:",
		"Generated commit message:\n",
	}
	
	for _, prefix := range prefixesToRemove {
		commitMessage = strings.TrimPrefix(commitMessage, prefix)
	}
	
	commitMessage = strings.TrimSpace(commitMessage)
	commitMessage = strings.TrimPrefix(commitMessage, "```")
	commitMessage = strings.TrimSuffix(commitMessage, "```")
	commitMessage = strings.TrimSpace(commitMessage)

	return commitMessage, nil
}
