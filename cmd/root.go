// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package cmd

import (
	"fmt"
	"github.com/elastic/aws-ami-share/common"
	"github.com/elastic/aws-ami-share/core"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"os"
)

const (
	CLIName    = "ami-share"
	CLIExample = "AWS_SDK_LOAD_CONFIG=true AWS_PROFILE=staging-ami ./ami-share -v -c example.yaml -p plan.yaml"
)

func RootCmd(version, hash, date string) {
	buildInfo := fmt.Sprintf("Version=%s, Build=%s, Date=%s", version, hash, date)
	var configFile string
	var verbose bool
	var params common.ShareParams

	var rootCmd = &cobra.Command{
		Use: CLIName,
		Long: fmt.Sprintf(`AWS AMI Share is a utility for sharing AMIs across accounts.
%s`, buildInfo),
		Example: CLIExample,
		Version: version,
	}

	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enables debug output.")
	rootCmd.PersistentFlags().BoolVar(&params.NoDryRun, "no-dry-run", false,
		"If specified, it shares AMIs. Otherwise it just list target candidates in plan file.")
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "",
		"(required) Path to the config file.")
	rootCmd.PersistentFlags().StringVarP(&params.PlanFile, "plan", "p", "",
		"(required) Path to output file for plan.")
	rootCmd.PersistentFlags().BoolVar(&params.ShareSnapshots, "share-snapshots", false,
		"(optional) Whether to share snapshots attached to AMIs.")

	if err := rootCmd.MarkPersistentFlagRequired("config"); err != nil {
		log.Infof("Failed with error: %v", err)
		os.Exit(1)
	}
	if err := rootCmd.MarkPersistentFlagRequired("plan"); err != nil {
		log.Infof("Failed with error: %v", err)
		os.Exit(1)
	}

	rootCmd.PreRun = func(cmd *cobra.Command, args []string) {
		log.SetLevel(log.InfoLevel)
		if verbose {
			log.SetLevel(log.DebugLevel)
		}
	}

	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		logger := log.WithFields(log.Fields{
			"context":   "share-command",
			"operation": "validation",
		})

		if config, err := common.LoadConfig(configFile); err != nil {
			logger.Errorf("Failed to parse config file: %v", err)
			return err
		} else {
			logger.Info("Validating config")
			if err := config.Validate(); err != nil {
				return err
			}
			params.Config = config
		}

		logger.Info("Initializing")
		shareAMI, err := core.NewAWSShareAMI(&params)
		if err != nil {
			return err
		}

		logger.Info("Validating accounts")
		if err := shareAMI.ValidateAccounts(); err != nil {
			return err
		}
		return shareAMI.Run()
	}
	err := rootCmd.Execute()
	if err != nil {
		log.Infof("Failed with error: %v", err)
		os.Exit(1)
	}
}
