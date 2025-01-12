/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package options

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	clientset "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	api "k8s.io/kubernetes/pkg/apis/core"
	apiv1 "k8s.io/kubernetes/pkg/apis/core/v1"
	"k8s.io/kubernetes/pkg/apis/core/validation"
)

type ClusterCapacityConfig struct {
	Pod        *v1.Pod
	KubeClient clientset.Interface
	RestConfig *restclient.Config
	Options    *ClusterCapacityOptions
}

type ClusterCapacityOptions struct {
	Kubeconfig                 string
	DefaultSchedulerConfigFile string
	MaxLimit                   int
	Verbose                    bool
	PodSpecFile                string
	OutputFormat               string
}

func NewClusterCapacityConfig(opt *ClusterCapacityOptions) *ClusterCapacityConfig {
	return &ClusterCapacityConfig{
		Options: opt,
	}
}

func NewClusterCapacityOptions() *ClusterCapacityOptions {
	return &ClusterCapacityOptions{}
}

func (s *ClusterCapacityOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&s.Kubeconfig, "kubeconfig", s.Kubeconfig, "Path to the kubeconfig file to use for the analysis.")
	fs.StringVar(&s.PodSpecFile, "podspec", s.PodSpecFile, "Path to JSON or YAML file containing pod definition.")
	fs.IntVar(&s.MaxLimit, "max-limit", 0, "Number of instances of pod to be scheduled after which analysis stops. By default unlimited.")

	//TODO(jchaloup): uncomment this line once the multi-schedulers are fully implemented
	//fs.StringArrayVar(&s.SchedulerConfigFile, "config", s.SchedulerConfigFile, "Paths to files containing scheduler configuration in JSON or YAML format")

	fs.StringVar(&s.DefaultSchedulerConfigFile, "default-config", s.DefaultSchedulerConfigFile, "Path to JSON or YAML file containing scheduler configuration.")

	fs.BoolVar(&s.Verbose, "verbose", s.Verbose, "Verbose mode")
	fs.StringVarP(&s.OutputFormat, "output", "o", s.OutputFormat, "Output format. One of: json|yaml (Note: output is not versioned or guaranteed to be stable across releases).")
}

func (s *ClusterCapacityConfig) ParseAPISpec(schedulerName string) error {
	var spec io.Reader
	var err error
	if strings.HasPrefix(s.Options.PodSpecFile, "http://") || strings.HasPrefix(s.Options.PodSpecFile, "https://") {
		response, err := http.Get(s.Options.PodSpecFile)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("unable to read URL %q, server reported %v, status code=%v", s.Options.PodSpecFile, response.Status, response.StatusCode)
		}
		spec = response.Body
	} else {
		filename, _ := filepath.Abs(s.Options.PodSpecFile)
		spec, err = os.Open(filename)
		if err != nil {
			return fmt.Errorf("Failed to open config file: %v", err)
		}
	}

	decoder := yaml.NewYAMLOrJSONDecoder(spec, 4096)
	versionedPod := &v1.Pod{}
	err = decoder.Decode(versionedPod)
	if err != nil {
		return fmt.Errorf("Failed to decode config file: %v", err)
	}

	if versionedPod.ObjectMeta.Namespace == "" {
		versionedPod.ObjectMeta.Namespace = "default"
	}

	// set pod's scheduler name to cluster-capacity
	if versionedPod.Spec.SchedulerName == "" {
		versionedPod.Spec.SchedulerName = schedulerName
	}

	// hardcoded from kube api defaults and validation
	// TODO: rewrite when object validation gets more available for non kubectl approaches in kube
	if versionedPod.Spec.DNSPolicy == "" {
		versionedPod.Spec.DNSPolicy = v1.DNSClusterFirst
	}
	if versionedPod.Spec.RestartPolicy == "" {
		versionedPod.Spec.RestartPolicy = v1.RestartPolicyAlways
	}

	for i := range versionedPod.Spec.Containers {
		if versionedPod.Spec.Containers[i].TerminationMessagePolicy == "" {
			versionedPod.Spec.Containers[i].TerminationMessagePolicy = v1.TerminationMessageFallbackToLogsOnError
		}
	}

	// TODO: client side validation seems like a long term problem for this command.
	internalPod := &api.Pod{}
	if err := apiv1.Convert_v1_Pod_To_core_Pod(versionedPod, internalPod, nil); err != nil {
		return fmt.Errorf("unable to convert to internal version: %#v", err)

	}
	if errs := validation.ValidatePodCreate(internalPod, validation.PodValidationOptions{}); len(errs) > 0 {
		var errStrs []string
		for _, err := range errs {
			errStrs = append(errStrs, fmt.Sprintf("%v: %v", err.Type, err.Field))
		}
		return fmt.Errorf("Invalid pod: %#v", strings.Join(errStrs, ", "))
	}

	s.Pod = versionedPod
	return nil
}
