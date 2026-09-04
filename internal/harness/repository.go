package harness

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/alexghr/pact/internal/state"
)

var ErrInvalidRepository = errors.New("invalid repository")

type repositoryCheckout interface {
	Clone(context.Context, state.Repository, string) error
}

type hostGitCheckout struct{}

func (hostGitCheckout) Clone(ctx context.Context, repository state.Repository, workspace string) error {
	command := exec.CommandContext(
		ctx,
		"git",
		"-c", "core.hooksPath=/dev/null",
		"clone",
		"--branch", repository.DefaultBranch,
		"--no-recurse-submodules",
		"--",
		repository.CloneURL,
		workspace,
	)
	command.Env = environmentWith(os.Environ(), "GIT_TERMINAL_PROMPT", "0")
	if err := command.Run(); err != nil {
		return fmt.Errorf("clone repository: %w", err)
	}
	return nil
}

func (r *Runner) CreateRepository(ctx context.Context, repository state.Repository) (int64, error) {
	repository, err := normalizeRepository(repository)
	if err != nil {
		return 0, err
	}
	return r.store.CreateRepository(ctx, repository)
}

func (r *Runner) createManagedSession(
	ctx context.Context,
	repositoryIDs []int64,
) (sessionID int64, workspace string, resultErr error) {
	repositories := make([]state.Repository, 0, len(repositoryIDs))
	for _, repositoryID := range repositoryIDs {
		repository, err := r.store.GetRepository(ctx, repositoryID)
		if err != nil {
			return 0, "", err
		}
		repositories = append(repositories, repository)
	}

	if r.storageRoot == "" {
		return 0, "", errors.New("storage root is required for a managed workspace")
	}
	sessionsRoot := filepath.Join(r.storageRoot, "sessions")
	if err := os.MkdirAll(sessionsRoot, 0700); err != nil {
		return 0, "", fmt.Errorf("create sessions directory: %w", err)
	}
	workspaceDir, err := os.MkdirTemp(sessionsRoot, "pact-session-")
	if err != nil {
		return 0, "", fmt.Errorf("create session workspace: %w", err)
	}
	keepWorkspace := false
	defer func() {
		if !keepWorkspace {
			if err := os.RemoveAll(workspaceDir); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("remove session workspace: %w", err))
			}
		}
	}()

	checkouts := make([]state.SessionRepository, 0, len(repositories))
	for _, repository := range repositories {
		if r.checkout == nil {
			return 0, "", errors.New("repository checkout is unavailable")
		}
		checkoutPath, err := os.MkdirTemp(workspaceDir, repository.Name+"-")
		if err != nil {
			return 0, "", fmt.Errorf("create checkout directory for repository %q: %w", repository.Name, err)
		}
		if err := r.checkout.Clone(ctx, repository, checkoutPath); err != nil {
			return 0, "", fmt.Errorf("prepare repository %q: %w", repository.Name, err)
		}
		checkouts = append(checkouts, state.SessionRepository{
			CheckoutDir: filepath.Base(checkoutPath),
			Repository:  repository,
		})
	}
	workspace, err = canonicalWorkspace(workspaceDir)
	if err != nil {
		return 0, "", err
	}
	if len(checkouts) == 0 {
		sessionID, err = r.store.CreateSession(ctx, workspace)
	} else {
		sessionID, err = r.store.CreateSessionForRepositories(ctx, workspace, checkouts)
	}
	if err != nil {
		return 0, "", err
	}
	keepWorkspace = true
	return sessionID, workspace, nil
}

func normalizeRepository(repository state.Repository) (state.Repository, error) {
	repository.URL = strings.TrimSpace(repository.URL)
	repository.CloneURL = strings.TrimSpace(repository.CloneURL)
	repository.PushURL = strings.TrimSpace(repository.PushURL)
	repository.Name = strings.TrimSpace(repository.Name)
	repository.DefaultBranch = strings.TrimSpace(repository.DefaultBranch)

	if repository.URL == "" {
		return state.Repository{}, invalidRepository("URL must not be empty")
	}
	if err := validateRepositoryURL(repository.URL); err != nil {
		return state.Repository{}, err
	}
	if repository.CloneURL == "" {
		repository.CloneURL = repository.URL
	}
	if err := validateGitURL("clone URL", repository.CloneURL); err != nil {
		return state.Repository{}, err
	}
	if repository.PushURL == "" {
		repository.PushURL = repository.CloneURL
	}
	if err := validateGitURL("push URL", repository.PushURL); err != nil {
		return state.Repository{}, err
	}
	if repository.Name == "" {
		repository.Name = repositoryName(repository.CloneURL)
	}
	if repository.Name == "" {
		return state.Repository{}, invalidRepository("name could not be derived from clone URL")
	}
	if repository.DefaultBranch == "" {
		repository.DefaultBranch = "main"
	}
	if strings.ContainsAny(repository.DefaultBranch, "\r\n") {
		return state.Repository{}, invalidRepository("default branch must not contain a newline")
	}
	return repository, nil
}

func validateRepositoryURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return invalidRepository("URL must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil {
		return invalidRepository("URL must not contain credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return invalidRepository("URL must not contain a query or fragment")
	}
	return nil
}

func validateGitURL(field, value string) error {
	if strings.ContainsAny(value, "\x00\r\n\t ") {
		return invalidRepository(field + " contains unsupported whitespace")
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" {
			return invalidRepository(field + " is not a valid remote URL")
		}
		if strings.HasPrefix(parsed.Hostname(), "-") {
			return invalidRepository(field + " has an invalid host")
		}
		switch parsed.Scheme {
		case "http", "https":
			if parsed.User != nil {
				return invalidRepository(field + " must not contain credentials")
			}
		case "ssh":
			if parsed.User != nil {
				if _, hasPassword := parsed.User.Password(); hasPassword {
					return invalidRepository(field + " must not contain a password")
				}
			}
		case "git":
			if parsed.User != nil {
				return invalidRepository(field + " must not contain credentials")
			}
		default:
			return invalidRepository(field + " uses an unsupported transport")
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return invalidRepository(field + " must not contain a query or fragment")
		}
		return nil
	}

	if strings.Contains(value, "::") {
		return invalidRepository(field + " uses an unsupported remote helper")
	}
	separator := strings.IndexByte(value, ':')
	if separator < 1 || separator == len(value)-1 || strings.Contains(value[:separator], "/") {
		return invalidRepository(field + " must use HTTP(S), SSH, Git, or SCP syntax")
	}
	host := value[:separator]
	if at := strings.LastIndexByte(host, '@'); at >= 0 {
		if at == 0 || at == len(host)-1 || !validSCPName(host[:at]) {
			return invalidRepository(field + " has invalid SCP syntax")
		}
		host = host[at+1:]
	}
	if !validSCPName(host) || strings.HasPrefix(host, "-") || !validSCPPath(value[separator+1:]) {
		return invalidRepository(field + " has invalid SCP syntax")
	}
	return nil
}

func validSCPName(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("._-", character) {
			continue
		}
		return false
	}
	return true
}

func validSCPPath(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("/._~+-", character) {
			continue
		}
		return false
	}
	return true
}

func repositoryName(cloneURL string) string {
	cloneURL = strings.TrimRightFunc(cloneURL, func(r rune) bool {
		return r == '/' || unicode.IsSpace(r)
	})
	if parsed, err := url.Parse(cloneURL); err == nil && parsed.Scheme != "" {
		cloneURL = parsed.Path
	} else if separator := strings.IndexByte(cloneURL, ':'); separator >= 0 {
		cloneURL = cloneURL[separator+1:]
	}
	name := path.Base(strings.TrimRight(cloneURL, "/"))
	name = strings.TrimSuffix(name, ".git")
	if name == "." || name == "/" {
		return ""
	}
	return name
}

func invalidRepository(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidRepository, message)
}

func environmentWith(environment []string, name, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}
