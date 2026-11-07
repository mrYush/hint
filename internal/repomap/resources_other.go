//go:build !linux && !darwin

package repomap

import (
	"context"
	"errors"
)

func localResourceSampler(string) ResourceSampler {
	return func(context.Context) (ResourceSample, error) {
		return ResourceSample{}, errors.New("index telemetry: this platform has no background resource adapter")
	}
}
