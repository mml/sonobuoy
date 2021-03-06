/*
Copyright 2017 Heptio Inc.

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

package config

import (
	"fmt"
	"os"

	"github.com/heptio/sonobuoy/pkg/buildinfo"
	"github.com/heptio/sonobuoy/pkg/plugin"
	pluginloader "github.com/heptio/sonobuoy/pkg/plugin/loader"
	"github.com/spf13/viper"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// LoadConfig will load the current sonobuoy configuration using the filesystem
// and environment variables, and returns a config object
func LoadConfig() (*Config, error) {
	var err error
	cfg := NewWithDefaults()

	// 0 - load defaults
	viper.SetConfigType("json")
	viper.SetConfigName("config")
	viper.AddConfigPath("/etc/sonobuoy/")
	viper.AddConfigPath(".")
	viper.SetDefault("kubeconfig", "")
	viper.BindEnv("kubeconfig")
	// Allow specifying a custom config file via the SONOBUOY_CONFIG env var
	if forceCfg := os.Getenv("SONOBUOY_CONFIG"); forceCfg != "" {
		viper.SetConfigFile(forceCfg)
	}

	// 1 - Read in the config file.
	if err = viper.ReadInConfig(); err != nil {
		return nil, err
	}

	// 2 - Unmarshal the Config struct
	if err = viper.Unmarshal(cfg); err != nil {
		return nil, err
	}

	// 3 - figure out what address we will tell pods to dial for aggregation
	if cfg.Aggregation.AdvertiseAddress == "" {
		if ip, ok := os.LookupEnv("SONOBUOY_ADVERTISE_IP"); ok {
			cfg.Aggregation.AdvertiseAddress = fmt.Sprintf("%v:%d", ip, cfg.Aggregation.BindPort)
		} else {
			hostname, _ := os.Hostname()
			if hostname != "" {
				cfg.Aggregation.AdvertiseAddress = fmt.Sprintf("%v:%d", hostname, cfg.Aggregation.BindPort)
			}
		}
	}

	// 4 - Any other settings
	cfg.Version = buildinfo.Version

	// Make the results dir overridable with an environment variable
	if resultsDir, ok := os.LookupEnv("RESULTS_DIR"); ok {
		cfg.ResultsDir = resultsDir
	}

	// Use the exact user config for resources, if set. Viper merges in
	// arrays, making this part necessary.  This way, if they leave out the
	// Resources section altogether they get the default set, but if they
	// set it at all (including to an empty array), we use exactly what
	// they specify.
	if viper.IsSet("Resources") {
		cfg.Resources = viper.GetStringSlice("Resources")
	}

	// 5 - Load any plugins we have
	err = loadAllPlugins(cfg)

	return cfg, err
}

// LoadClient creates a kube-clientset, using given sonobuoy configuration
func LoadClient(cfg *Config) (kubernetes.Interface, error) {
	var config *rest.Config
	var err error

	// 1 - gather config information used to initialize
	kubeconfig := viper.GetString("kubeconfig")
	if len(kubeconfig) > 0 {
		cfg.Kubeconfig = kubeconfig
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, err
	}

	// 2 - creates the clientset from kubeconfig
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}

// loadAllPlugins takes the given sonobuoy configuration and gives back a
// plugin.Interface for every plugin specified by the configuration.
func loadAllPlugins(cfg *Config) error {
	var plugins []plugin.Interface

	// Load all Plugins
	plugins, err := pluginloader.LoadAllPlugins(cfg.PluginNamespace, cfg.PluginSearchPath, cfg.PluginSelections, cfg.Aggregation.AdvertiseAddress)
	if err != nil {
		return err
	}

	// Find any selected plugins that weren't loaded
	for _, sel := range cfg.PluginSelections {
		found := false
		for _, p := range plugins {
			if p.GetName() == sel.Name {
				found = true
			}
		}

		if !found {
			return fmt.Errorf("Configured plugin %v does not exist", sel.Name)
		}
	}

	for _, p := range plugins {
		cfg.addPlugin(p)
	}

	return nil
}
