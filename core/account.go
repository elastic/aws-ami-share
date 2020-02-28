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
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/sts"
	"github.com/elastic/aws-ami-share/common"
	"github.com/elastic/aws-ami-share/utils"
	log "github.com/sirupsen/logrus"
)

const (
	DefaultRegion = "us-east-1"
)

func AccountSessionKey(account *common.Account, region string) utils.SessionKey {
	return utils.SessionKey{AccountID: account.ID, AssumeRole: account.AssumeRole, Region: region}
}

func GetAccount(sessionFactory *utils.AWSSessionFactory, configAccount *common.Account) (common.Account, error) {
	logger := log.WithFields(log.Fields{
		"profile":   configAccount.ID,
		"operation": "account-info",
	})

	var account common.Account
	sess, err := sessionFactory.GetSession(AccountSessionKey(configAccount, DefaultRegion))
	if err != nil {
		return account, err
	}

	identityOutput, err := sts.New(sess).GetCallerIdentity(nil)
	if err != nil {
		logger.Errorf("failed to get caller identity for account")
		return account, err
	}
	account.ID = *identityOutput.Account

	globalSession, err := sessionFactory.GetSession(AccountSessionKey(configAccount, DefaultRegion))
	if err != nil {
		logger.Errorf("failed to create default session in %s", DefaultRegion)
		return account, err
	}
	aliasesOutput, err := iam.New(globalSession).ListAccountAliases(nil)
	if err != nil {
		logger.Errorf("failed to get account alias")
		return account, err
	}
	account.Alias = *aliasesOutput.AccountAliases[0]

	return account, nil
}

func ValidateAccount(sessionFactory *utils.AWSSessionFactory, account *common.Account) error {
	expectedAccount, err := GetAccount(sessionFactory, account)
	if err != nil {
		return err
	}

	if expectedAccount.ID != account.ID {
		return errors.New(fmt.Sprintf("account number does not match. Expected: %s got %s",
			expectedAccount.ID, account.ID))
	}

	if expectedAccount.Alias != account.Alias {
		return errors.New(fmt.Sprintf("account alias does not match. Expected: %s got %s",
			expectedAccount.Alias, account.Alias))
	}

	return nil
}
