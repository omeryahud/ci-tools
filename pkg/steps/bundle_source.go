package steps

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	buildapi "github.com/openshift/api/build/v1"
	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type bundleSourceStep struct {
	config      api.BundleSourceStepConfiguration
	resources   api.ResourceConfiguration
	buildClient BuildClient
	imageClient imageclientset.ImageStreamsGetter
	istClient   imageclientset.ImageStreamTagsGetter
	jobSpec     *api.JobSpec
	artifactDir string
	dryLogger   *DryLogger
	pullSecret  *coreapi.Secret
}

func (s *bundleSourceStep) Inputs(dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func (s *bundleSourceStep) Run(ctx context.Context, dry bool) error {
	return results.ForReason("building_bundle_source").ForError(s.run(ctx, dry))
}

func (s *bundleSourceStep) run(ctx context.Context, dry bool) error {
	source := fmt.Sprintf("%s:%s", api.PipelineImageStream, api.PipelineImageStreamTagReferenceSource)
	var workingDir string
	if dry {
		workingDir = "dry-fake"
	} else {
		var err error
		workingDir, err = getWorkingDir(s.istClient, source, s.jobSpec.Namespace())
		if err != nil {
			return fmt.Errorf("failed to get workingDir: %w", err)
		}
	}
	dockerfile, err := s.bundleSourceDockerfile(dry)
	if err != nil {
		return err
	}
	build := buildFromSource(
		s.jobSpec, api.PipelineImageStreamTagReferenceSource, s.config.To,
		buildapi.BuildSource{
			Type:       buildapi.BuildSourceDockerfile,
			Dockerfile: &dockerfile,
			Images: []buildapi.ImageSource{
				{
					From: coreapi.ObjectReference{
						Kind: "ImageStreamTag",
						Name: source,
					},
					Paths: []buildapi.ImageSourcePath{{
						SourcePath:     fmt.Sprintf("%s/%s/.", workingDir, s.config.ContextDir),
						DestinationDir: ".",
					}},
				},
			},
		},
		"",
		s.resources,
		s.pullSecret,
	)
	return handleBuild(ctx, s.buildClient, build, dry, s.artifactDir, s.dryLogger)
}

func replaceCommand(manifestDir, pullSpec, with string) string {
	return fmt.Sprintf("find %s -type f -exec sed -i 's?%s?%s?g' {} +", manifestDir, pullSpec, with)
}

func (s *bundleSourceStep) bundleSourceDockerfile(dry bool) (string, error) {
	var dockerCommands []string
	dockerCommands = append(dockerCommands, "")
	dockerCommands = append(dockerCommands, fmt.Sprintf("FROM %s:%s", api.PipelineImageStream, api.PipelineImageStreamTagReferenceSource))
	manifestDir := filepath.Join(s.config.ContextDir, s.config.OperatorManifests)
	for _, sub := range s.config.Substitute {
		replaceSpec, err := s.getFullPullSpec(sub.With, dry)
		if err != nil {
			return "", fmt.Errorf("failed to get replacement imagestream for image tag `%s`", sub.With)
		}
		dockerCommands = append(dockerCommands, fmt.Sprintf(`RUN ["bash", "-c", "%s"]`, replaceCommand(manifestDir, sub.PullSpec, replaceSpec)))
	}
	dockerCommands = append(dockerCommands, "")
	return strings.Join(dockerCommands, "\n"), nil
}

func (s *bundleSourceStep) getFullPullSpec(tag string, dry bool) (string, error) {
	if dry {
		return "dry-registry.ci.openshift.org/namespace/stable:" + tag, nil
	}
	is, err := s.imageClient.ImageStreams(s.jobSpec.Namespace()).Get(api.StableImageStream, meta.GetOptions{})
	if err != nil {
		return "", err
	}
	if len(is.Status.PublicDockerImageRepository) > 0 {
		return is.Status.PublicDockerImageRepository + ":" + tag, nil
	}
	if len(is.Status.DockerImageRepository) > 0 {
		return is.Status.DockerImageRepository + ":" + tag, nil
	}
	return "", fmt.Errorf("no pull spec available for image stream %s", api.StableImageStream)
}

func (s *bundleSourceStep) Requires() []api.StepLink {
	return []api.StepLink{
		api.InternalImageLink(api.PipelineImageStreamTagReferenceSource),
	}
}

func (s *bundleSourceStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func (s *bundleSourceStep) Provides() (api.ParameterMap, api.StepLink) {
	return api.ParameterMap{}, api.InternalImageLink(s.config.To)
}

func (s *bundleSourceStep) Name() string { return string(s.config.To) }

func (s *bundleSourceStep) Description() string {
	return fmt.Sprintf("Build image %s from the repository", s.config.To)
}

func BundleSourceStep(config api.BundleSourceStepConfiguration, resources api.ResourceConfiguration, buildClient BuildClient, imageClient imageclientset.ImageStreamsGetter, istClient imageclientset.ImageStreamTagsGetter, artifactDir string, jobSpec *api.JobSpec, dryLogger *DryLogger, pullSecret *coreapi.Secret) api.Step {
	return &bundleSourceStep{
		config:      config,
		resources:   resources,
		buildClient: buildClient,
		imageClient: imageClient,
		istClient:   istClient,
		artifactDir: artifactDir,
		jobSpec:     jobSpec,
		dryLogger:   dryLogger,
		pullSecret:  pullSecret,
	}
}

// BundleSourceName returns the PipelineImageStreamTagReference for the source image for the given bundle image tag reference
func BundleSourceName(bundleName api.PipelineImageStreamTagReference) api.PipelineImageStreamTagReference {
	return api.PipelineImageStreamTagReference(fmt.Sprintf("%s-sub", bundleName))
}