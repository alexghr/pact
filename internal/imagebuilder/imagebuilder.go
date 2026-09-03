package imagebuilder

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/alexghr/pact/internal/docker"
)

type Resolver interface {
	HasProfile(string) bool
	Resolve(context.Context, string) (string, error)
}

type Profile struct {
	Name  string
	Build docker.BuildOptions
}

func BuiltinProfiles() []Profile {
	return []Profile{
		{
			Name: "generic",
			Build: docker.BuildOptions{
				ContextDir: "./container",
				Dockerfile: filepath.Join("container", "generic.Dockerfile"),
				Tag:        "pact-codex:generic",
			},
		},
		{
			Name: "go",
			Build: docker.BuildOptions{
				ContextDir: "./container",
				Dockerfile: filepath.Join("container", "go.Dockerfile"),
				Tag:        "pact-codex:go",
			},
		},
	}
}

type OnDemand struct {
	builder      docker.ImageBuilder
	profiles     map[string]Profile
	mu           sync.Mutex
	preparations map[string]*preparation
}

type preparation struct {
	done chan struct{}
	err  error
}

func NewOnDemand(builder docker.ImageBuilder, profiles []Profile) *OnDemand {
	return &OnDemand{
		builder:      builder,
		profiles:     profileMap(profiles),
		preparations: make(map[string]*preparation),
	}
}

func (b *OnDemand) HasProfile(name string) bool {
	_, ok := b.profiles[name]
	return ok
}

func (b *OnDemand) Resolve(ctx context.Context, name string) (string, error) {
	profile, ok := b.profiles[name]
	if !ok {
		return "", unsupportedProfile(name)
	}
	fingerprint, err := buildFingerprint(profile)
	if err != nil {
		return "", fmt.Errorf("fingerprint image profile %q: %w", name, err)
	}
	image := profile.Build.Tag + "-" + fingerprint
	preparation, prepare := b.begin(image)
	if !prepare {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-preparation.done:
			return image, preparation.err
		}
	}

	exists, err := b.builder.ImageExists(ctx, image)
	if err == nil && !exists {
		options := profile.Build
		options.Tag = image
		err = b.builder.Build(ctx, options)
	}
	if err != nil {
		err = fmt.Errorf("prepare image profile %q: %w", name, err)
	}
	b.finish(image, preparation, err)
	return image, err
}

func (b *OnDemand) begin(image string) (*preparation, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if current, ok := b.preparations[image]; ok {
		return current, false
	}
	current := &preparation{done: make(chan struct{})}
	b.preparations[image] = current
	return current, true
}

func (b *OnDemand) finish(image string, preparation *preparation, err error) {
	b.mu.Lock()
	preparation.err = err
	delete(b.preparations, image)
	close(preparation.done)
	b.mu.Unlock()
}

func buildFingerprint(profile Profile) (string, error) {
	digest := sha256.New()
	dockerfile, err := filepath.Rel(profile.Build.ContextDir, profile.Build.Dockerfile)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(digest, "dockerfile\x00%s\x00", filepath.ToSlash(dockerfile))
	dockerfileDigest, err := hashFile(profile.Build.Dockerfile)
	if err != nil {
		return "", err
	}
	digest.Write(dockerfileDigest)

	err = filepath.WalkDir(profile.Build.ContextDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(profile.Build.ContextDir, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(digest, "%s\x00%s\x00", filepath.ToSlash(relative), info.Mode())
		contentDigest := sha256.New()
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if _, err := io.WriteString(contentDigest, target); err != nil {
				return err
			}
			digest.Write(contentDigest.Sum(nil))
			return nil
		}
		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(contentDigest, file)
			if err := errors.Join(copyErr, file.Close()); err != nil {
				return err
			}
		}
		if _, err := digest.Write(contentDigest.Sum(nil)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

func hashFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	digest := sha256.New()
	_, copyErr := io.Copy(digest, file)
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return nil, err
	}
	return digest.Sum(nil), nil
}

func profileMap(profiles []Profile) map[string]Profile {
	result := make(map[string]Profile, len(profiles))
	for _, profile := range profiles {
		result[profile.Name] = profile
	}
	return result
}

func unsupportedProfile(name string) error {
	return fmt.Errorf("unsupported image %q", name)
}
