package docker

import (
	"context"
)

func CreateVolume(ctx context.Context, name string, labels map[string]string) error {
	_, err := output(ctx, append(append([]string{"volume", "create"}, labelsArgs(labels)...), name)...)
	return err
}

func RemoveVolume(ctx context.Context, name string) error {
	_, err := output(ctx, "volume", "rm", name)
	return err
}
