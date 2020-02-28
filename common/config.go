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

package common

import (
	"bytes"
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"strings"
	"text/template"
)

type ShareParams struct {
	Config         *Config
	NoDryRun       bool
	ShareSnapshots bool
	PlanFile       string
}

type Filter struct {
	Property string `yaml:"property"`
	Value    string `yaml:"value"`
	Invert   bool   `yaml:"invert"`
}

type AMISelection struct {
	Copy    bool     `yaml:"copy"` // TODO: flag for copying AMIs not implemented yet
	Regions []string `yaml:"regions"`
	Filters []Filter `yaml:"filters"`
}

type Account struct {
	ID            string                  `yaml:"id"`
	Alias         string                  `yaml:"alias"`
	AssumeRole    string                  `yaml:"assume-role"`
	PostShareTags map[string]string       `yaml:"post-share-tags,omitempty"`
	Regions       []string                `yaml:"regions,omitempty"`
	AMIs          map[string]AMISelection `yaml:"amis,omitempty"`
}

type Config struct {
	regions        []string
	SourceAccount  Account   `yaml:"source-account"`
	TargetAccounts []Account `yaml:"target-accounts"`
}

func GetEnvironmentVars() map[string]string {
	env := make(map[string]string)
	for _, envVar := range os.Environ() {
		pair := strings.Split(envVar, "=")
		env[pair[0]] = pair[1]
	}
	return env
}

func LoadConfig(path string) (*Config, error) {
	logger := log.WithFields(log.Fields{
		"context":   "config-load",
		"operation": "validation",
	})

	var err error
	configRaw, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	env := GetEnvironmentVars()
	temp := template.Must(template.New("ConfigTemplate").Parse(string(configRaw)))

	var resolvedConfigRaw bytes.Buffer
	err = temp.Execute(&resolvedConfigRaw, env)
	if err != nil {
		return nil, err
	}
	logger.Debug("Resolved config:")
	logger.Debug(resolvedConfigRaw.String())

	config := new(Config)
	err = yaml.UnmarshalStrict(resolvedConfigRaw.Bytes(), config)
	if err != nil {
		return nil, err
	}
	return config, nil
}

func (config *Config) Validate() error {
	if len(config.SourceAccount.Regions) > 0 || len(config.SourceAccount.AMIs) > 0 {
		return errors.New("fields [regions] and/or [amis] not allowed on registry account")
	}

	if config.SourceAccount.AssumeRole == "" {
		return errors.New("assume-role must be specified on source account")
	}

	for _, account := range config.TargetAccounts {
		if len(account.AMIs) < 1 {
			return errors.New(fmt.Sprintf("account [%s] does not have any AMIs: required at least one", account.Alias))
		}

		if len(account.PostShareTags) > 0 {
			return errors.New(fmt.Sprintf("post-share-tags not allowed here: account [%s]", account.Alias))
		}

		if account.AssumeRole == "" {
			return errors.New(fmt.Sprintf("assume-role must be specified on [%s]", account.Alias))
		}
	}
	config.CreateRoleARNs()

	return nil
}

// role ARN format: arn:aws:iam::account-id:role/role-name
func (account *Account) GenerateRoleARN() {
	if !strings.HasPrefix(account.AssumeRole, "arn:aws:iam::") {
		account.AssumeRole = fmt.Sprintf("arn:aws:iam::%s:role/%s", account.ID, account.AssumeRole)
	}
}

func (config *Config) CreateRoleARNs() {
	config.SourceAccount.GenerateRoleARN()
	for i := range config.TargetAccounts {
		config.TargetAccounts[i].GenerateRoleARN()
	}
	return
}

func (config *Config) ScanRegions() []string {
	// Use internal cache list if populated
	if len(config.regions) > 0 {
		return config.regions
	}
	uniqueRegionsMap := make(map[string]struct{})
	for _, account := range config.TargetAccounts {
		for _, region := range account.Regions {
			uniqueRegionsMap[region] = struct{}{}
		}
		for _, amis := range account.AMIs {
			for _, region := range amis.Regions {
				uniqueRegionsMap[region] = struct{}{}
			}
		}
	}

	var regions []string
	for region := range uniqueRegionsMap {
		regions = append(regions, region)
	}
	config.regions = regions
	return regions
}

func (filter Filter) String() string {
	var invertText string
	if filter.Invert {
		invertText = "(inverted)"
	}
	return fmt.Sprintf("{%s=%s%s}", filter.Property, filter.Value, invertText)
}
