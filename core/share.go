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

package core

import (
	"fmt"
	"github.com/elastic/aws-ami-share/common"
	"github.com/elastic/aws-ami-share/utils"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"io/ioutil"
)

const (
	ShareWithPrefix = "ShareWith"
	All             = "all"
)

type ImagesByRegion map[string]common.Images
type ImagesByGroup map[string]ImagesByRegion

type AMISharePlanAccount struct {
	ID         string        `yaml:"id"`
	Alias      string        `yaml:"alias"`
	AssumeRole string        `yaml:"assume-role"`
	AMIs       ImagesByGroup `yaml:"amis"`
}

type AMISharePlan struct {
	SourceAccount  AMISharePlanAccount   `yaml:"source-account"`
	TargetAccounts []AMISharePlanAccount `yaml:"target-accounts"`
}

type AWSShareAMI struct {
	ShareParams    *common.ShareParams
	logger         *log.Entry
	sessionFactory *utils.AWSSessionFactory
}

func NewAWSShareAMI(params *common.ShareParams) (AWSShareAMI, error) {
	shareAMI := AWSShareAMI{
		ShareParams: params,
		logger: log.WithFields(log.Fields{
			"context":   "aws-share-ami",
			"operation": "share",
		}),
	}
	sessionFactory := utils.NewAWSSessionFactory()
	shareAMI.sessionFactory = sessionFactory
	_, err := sessionFactory.GenerateMasterSession(AccountSessionKey(&params.Config.SourceAccount, DefaultRegion))
	return shareAMI, err
}

func (shareAMI *AWSShareAMI) ValidateAccounts() error {
	shareAMI.logger.Infof("Validating source account")
	err := ValidateAccount(shareAMI.sessionFactory, &shareAMI.ShareParams.Config.SourceAccount)
	if err != nil {
		return err
	}

	for _, account := range shareAMI.ShareParams.Config.TargetAccounts {
		shareAMI.logger.Infof("Validating account: %v", account.ID)
		err := ValidateAccount(shareAMI.sessionFactory, &account)
		if err != nil {
			return err
		}
	}
	return nil
}

func (shareAMI *AWSShareAMI) ScanForAMIs(account *common.Account) (ImagesByRegion, error) {
	regionImages := make(ImagesByRegion)
	config := shareAMI.ShareParams.Config
	for _, region := range config.ScanRegions() {
		sess, err := shareAMI.sessionFactory.GetSession(AccountSessionKey(account, region))
		if err != nil {
			return regionImages, err
		}

		if images, err := ListAMIs(sess); err != nil {
			return regionImages, err
		} else {
			regionImages[region] = images
		}
	}
	return regionImages, nil
}

func (shareAMI *AWSShareAMI) FilterAMIs(sourceImages ImagesByRegion, account common.Account) (ImagesByGroup, error) {
	groupedImages := make(ImagesByGroup)
	accountRegions := account.Regions
	for group, ami := range account.AMIs {
		shareAMI.logger.Infof("Processing %s AMIs", group)
		groupRegions := ami.Regions
		if len(groupRegions) < 1 {
			groupRegions = accountRegions
		}

		regionImages := make(ImagesByRegion)
		for _, region := range groupRegions {
			filteredImages := common.ApplyFilters(sourceImages[region], ami.Filters)
			shareAMI.logger.Debugf("Filters for %s: [%v] AMIs in [%s]", group, ami.Filters, region)
			shareAMI.logger.Infof("Found %v %s AMIs in [%s]", len(filteredImages), group, region)
			shareAMI.logger.Debugf("Filtered %s AMIs in [%s] => %s", group, region, filteredImages)
			regionImages[region] = filteredImages

		}
		groupedImages[group] = regionImages
	}

	return groupedImages, nil
}

func (shareAMI *AWSShareAMI) Run() error {
	shareAMI.logger.Infof("Generating plan for sharing AMIs")
	plan := new(AMISharePlan)
	config := shareAMI.ShareParams.Config
	imagesByRegion, err := shareAMI.ScanForAMIs(&config.SourceAccount)
	if err != nil {
		return err
	}
	shareAMI.logger.Debugf("AMIs in source account: %v", imagesByRegion)

	plan.SourceAccount = AMISharePlanAccount{
		ID:         config.SourceAccount.ID,
		Alias:      config.SourceAccount.Alias,
		AssumeRole: config.SourceAccount.AssumeRole,
		AMIs:       ImagesByGroup{All: imagesByRegion},
	}

	for _, account := range config.TargetAccounts {
		imagesToShare, _ := shareAMI.FilterAMIs(imagesByRegion, account)
		shareAMI.logger.Infof("Account: %v", imagesToShare)
		plan.TargetAccounts = append(plan.TargetAccounts, AMISharePlanAccount{
			ID:         account.ID,
			Alias:      account.Alias,
			AssumeRole: account.AssumeRole,
			AMIs:       imagesToShare,
		})
	}
	shareAMI.logger.Debugf("Plan for sharing: %v", plan)
	if err := shareAMI.WritePlan(plan); err != nil {
		return nil
	}

	if shareAMI.ShareParams.NoDryRun {
		shareAMI.logger.Infof("Running plan for sharing AMIs")
		// Iterate over all target accounts by region
		// For a given region share each the AMIs that were previously filtered in plan
		// Copy over tags for each AMI and mark AMI as shared usign post-sharing tags
		for _, account := range plan.TargetAccounts {
			for amiGroup, amisByRegion := range account.AMIs {
				for region, amis := range amisByRegion {
					for _, ami := range amis {
						shareAMI.logger.Infof("Sharing AMI %s[%s] with account [%s] in region [%s]", amiGroup, ami.String(), account.ID, region)
						err := ami.ShareWithAccount(account.ID, shareAMI.ShareParams.ShareSnapshots)
						if err != nil {
							shareAMI.logger.Errorf("Failed to share AMI [%s] with account: %s. Error: %s", ami.String(), account.ID, err)
							break
						}
						shareMetaTags := map[string]string{fmt.Sprintf("%s-%s", ShareWithPrefix, account.Alias): "1"}
						err = ami.AddTags(shareMetaTags, shareAMI.ShareParams.ShareSnapshots)
						if err != nil {
							shareAMI.logger.Errorf("Failed to add meta post-share tags to AMI [%s] in account: [%s]. Error: %s", ami.String(), account.ID, err)
						}

						if len(config.SourceAccount.PostShareTags) > 0 {
							err = ami.AddTags(config.SourceAccount.PostShareTags, true)
							if err != nil {
								shareAMI.logger.Errorf("Failed to add post-share tags to AMI [%s] in account: [%s]. Error: %s", ami.String(), account.ID, err)
							}
						}

						sess, err := shareAMI.sessionFactory.GetSession(utils.SessionKey{AccountID: account.ID, AssumeRole: account.AssumeRole, Region: region})
						if err != nil {
							shareAMI.logger.Errorf("Failed to get session for account: %s in region [%s]. Error: %s", account.ID, region, err)
							break
						}

						err = ami.CopyTags(sess, shareAMI.ShareParams.ShareSnapshots)
						if err != nil {
							shareAMI.logger.Errorf("Failed to copy tags for AMI [%s] in account: %s. Error: %s", ami.String(), account.ID, err)
							break
						}
					}
				}
			}
		}
	} else {
		shareAMI.logger.Infof("Would share AMIs in plan: %v", shareAMI.ShareParams.PlanFile)
	}

	return nil
}

func (shareAMI *AWSShareAMI) WritePlan(plan *AMISharePlan) error {
	raw, err := yaml.Marshal(plan)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(shareAMI.ShareParams.PlanFile, raw, 0644)
	shareAMI.logger.Infof("Wrote plan to: %s", shareAMI.ShareParams.PlanFile)
	return nil
}
