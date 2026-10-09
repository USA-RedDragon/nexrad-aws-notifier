package config

import (
	"github.com/USA-RedDragon/configulator/v2"
	cpflag "github.com/USA-RedDragon/configulator/v2/flags/pflag"
	"github.com/goccy/go-yaml"
	"github.com/spf13/pflag"
)

// New builds the loader for Config and registers its flags, --config/-c
// included, on fs. Values come from flags, then HTTP_PORT style environment
// variables, then config.yaml or the --config file, then the defaults.
func New(fs *pflag.FlagSet) *configulator.Configulator[Config] {
	c := configulator.New(ConfigSchema()).
		WithEnvironmentVariables(&configulator.EnvironmentVariableOptions{
			Prefix:    "",
			Separator: "_",
		}).
		WithFile(&configulator.FileOptions{
			Search: []string{"config.yaml"},
			Decoders: configulator.Decoders{
				".yaml": yaml.Unmarshal,
				".yml":  yaml.Unmarshal,
			},
		})
	return cpflag.Bind(c, fs, ConfigPFlagHooks(), nil)
}
