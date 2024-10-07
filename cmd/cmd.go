/*
Copyright © 2019 Itay Shakury @itaysk

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
package cmd

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"unicode"

	"github.com/ghodss/yaml"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var outputFormat *string
var inputFile *string
var configFile *string

func init() {
	outputFormat = rootCmd.PersistentFlags().StringP("output", "o", "yaml", "output format: yaml or json")
	inputFile = rootCmd.Flags().StringP("file", "f", "-", "file path to neat, or - to read from stdin")
	configFile = rootCmd.Flags().StringP("config", "c", "", "file path of config")
	viper.BindPFlag("config", rootCmd.Flags().Lookup("config"))

	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	rootCmd.MarkFlagFilename("file")
	rootCmd.AddCommand(getCmd)
	rootCmd.AddCommand(versionCmd)

}

func initConfig() {
	viper.SetConfigName("kubectl-neat")
	viper.SetConfigType("yaml")

	// Search current directory for config file
	viper.AddConfigPath(".")

	// Search $HOME/.config/ for config file
	home, err := os.UserHomeDir()
	if err == nil {
		viper.AddConfigPath(home + "/.config/")
	}

	// If a config file was explicitly specified, use only this file
	confFile := viper.GetString("config")
	if confFile != "" {
		viper.SetConfigFile(confFile)
	}

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			fmt.Println("No config file found") //TODO
		} else {
			// Config file was found but another error was produced
			log.Fatalf("Error reading in config: %s", err)
		}
	}
}

// Execute is the entry point for the command package
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use: "kubectl-neat",
	Example: `kubectl get pod mypod -o yaml | kubectl neat
kubectl get pod mypod -oyaml | kubectl neat -o json
kubectl get pod mypod -c ~/.config/kubectl-neat.conf | kubectl neat -o json
kubectl neat -f - <./my-pod.json
kubectl neat -f ./my-pod.json
kubectl neat -f ./my-pod.json --output yaml
kubectl neat -f ./my-pod.json --output yaml --config ~/.config/kubectl-neat.conf`,
	RunE: func(cmd *cobra.Command, args []string) error {
		initConfig()
		var in, out []byte
		var err error
		if *inputFile == "-" {
			stdin := cmd.InOrStdin()
			in, err = io.ReadAll(stdin)
			if err != nil {
				return err
			}
		} else {
			in, err = os.ReadFile(*inputFile)
			if err != nil {
				return err
			}
		}
		outFormat := *outputFormat
		if !cmd.Flag("output").Changed {
			outFormat = "same"
		}
		out, err = NeatYAMLOrJSON(in, outFormat)
		if err != nil {
			return err
		}
		cmd.Print(string(out))
		return nil
	},
}

var kubectl string = "kubectl"

var getCmd = &cobra.Command{
	Use: "get",
	Example: `kubectl neat get -- pod mypod -oyaml
kubectl neat get -- svc -n default myservice --output json`,
	FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true}, //don't try to validate kubectl get's flags
	RunE: func(cmd *cobra.Command, args []string) error {
		var out []byte
		var err error
		//reset defaults
		//there are two output settings in this subcommand: kubectl get's and kubectl-neat's
		//any combination of those can be provided by using the output flag in either side of the --
		//the most efficient is kubectl: json, kubectl-neat: yaml
		//0--0->Y--J #choose what's best for us
		//0--Y->Y--Y #user did specify output in kubectl, so respect that
		//0--J->J--J #user did specify output in kubectl, so respect that
		//Y--0->Y--J #user doesn't care about kubectl so use json but convert back
		//J--0->J--J #user expects json so use it for foth
		//if the user specified both side we can't touch it

		//the desired kubectl get output is always json, unless it was explicitly set by the user to yaml in which case the arg is overriden when concatenating the args later
		cmdArgs := append([]string{"get", "-o", "json"}, args...)
		kubectlCmd := exec.Command(kubectl, cmdArgs...)
		kres, err := kubectlCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("Error invoking kubectl as %v %v", kubectlCmd.Args, err)
		}
		//handle the case of 0--J->J--J
		outFormat := *outputFormat
		kubeout := "yaml"
		for _, arg := range args {
			if arg == "json" || arg == "ojson" {
				outFormat = "json"
			}
		}
		if !cmd.Flag("output").Changed && kubeout == "json" {
			outFormat = "json"
		}
		out, err = NeatYAMLOrJSON(kres, outFormat)
		if err != nil {
			return err
		}
		cmd.Println(string(out))
		return nil
	},
}

// populated by goreleaser
var (
	Version = "v0.0.0+unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print kubectl-neat version",
	Long:  "Print the version of kubectl-neat",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("kubectl-neat version: %s\n", Version)
	},
}

func isJSON(s []byte) bool {
	return bytes.HasPrefix(bytes.TrimLeftFunc(s, unicode.IsSpace), []byte{'{'})
}

// NeatYAMLOrJSON converts 'in' to json if needed, invokes neat, and converts back if needed according the the outputFormat argument: yaml/json/same
func NeatYAMLOrJSON(in []byte, outputFormat string) (out []byte, err error) {
	var injson, outjson string
	itsYaml := !isJSON(in)
	if itsYaml {
		injsonbytes, err := yaml.YAMLToJSON(in)
		if err != nil {
			return nil, fmt.Errorf("error converting from yaml to json : %v", err)
		}
		injson = string(injsonbytes)
	} else {
		injson = string(in)
	}

	outjson, err = Neat(injson)
	if err != nil {
		return nil, fmt.Errorf("error neating : %v", err)
	}

	if outputFormat == "yaml" || (outputFormat == "same" && itsYaml) {
		out, err = yaml.JSONToYAML([]byte(outjson))
		if err != nil {
			return nil, fmt.Errorf("error converting from json to yaml : %v", err)
		}
	} else {
		out = []byte(outjson)
	}
	return
}
