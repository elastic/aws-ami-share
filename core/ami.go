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
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/elastic/aws-ami-share/common"
	"github.com/rebuy-de/aws-nuke/pkg/types"
	log "github.com/sirupsen/logrus"
	"strings"
	"time"
)

var (
	logger = log.WithFields(log.Fields{"context": "aws-amis"})
)

type EC2Image struct {
	svc          *ec2.EC2
	id           string
	date         time.Time
	dateStr      string
	name         string
	tags         []*ec2.Tag
	tagsStr      string
	snapshots    []string
	snapshotTags map[string][]*ec2.Tag
}

// List AMIs from the given AWS session
// the session is attached to an AWS account and region
func ListAMIs(sess *session.Session) (common.Images, error) {
	var images common.Images
	svc := ec2.New(sess)
	params := &ec2.DescribeImagesInput{
		Owners: []*string{
			aws.String("self"),
		},
	}
	resp, err := svc.DescribeImages(params)
	if err != nil {
		return images, err
	}

	for _, out := range resp.Images {
		var snapshots []string
		snapshotTags := make(map[string][]*ec2.Tag)
		for _, blockDevice := range out.BlockDeviceMappings {
			if blockDevice == nil || blockDevice.Ebs == nil {
				logger.Debugf("Skipping block device: %v, because no snapshot to share", blockDevice)
				continue
			}
			snapshotId := aws.StringValue(blockDevice.Ebs.SnapshotId)
			snapshots = append(snapshots, snapshotId)
			tagsOutput, err := svc.DescribeTags(&ec2.DescribeTagsInput{
				Filters: []*ec2.Filter{
					{
						Name: aws.String("resource-id"),
						Values: []*string{
							aws.String(snapshotId),
						},
					},
				},
			})
			if err != nil {
				return images, err
			}

			var tags []*ec2.Tag
			for _, tagDesc := range tagsOutput.Tags {
				// Filter out meta tags added by this utility
				if strings.HasPrefix(aws.StringValue(tagDesc.Key), ShareWithPrefix) {
					continue
				}
				tags = append(tags, &ec2.Tag{Key: tagDesc.Key, Value: tagDesc.Value})
			}
			snapshotTags[snapshotId] = tags
		}

		var filteredTags []*ec2.Tag
		for _, tag := range out.Tags {
			// Filter out meta tags added by this utility
			if strings.HasPrefix(aws.StringValue(tag.Key), ShareWithPrefix) {
				continue
			}
			filteredTags = append(filteredTags, tag)
		}

		date, _ := time.Parse(time.RFC3339, *out.CreationDate)
		images = append(images, &EC2Image{
			svc:          svc,
			date:         date,
			dateStr:      *out.CreationDate,
			id:           *out.ImageId,
			name:         *out.Name,
			tags:         filteredTags,
			snapshots:    snapshots,
			snapshotTags: snapshotTags,
		})
	}

	return images, nil
}

// Copy tags to target account via AWS session
// the session is attached to an AWS account and region
func (e *EC2Image) CopyTags(sess *session.Session, shareSnapshots bool) error {
	svc := ec2.New(sess)
	_, err := svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{
			aws.String(e.id),
		},
		Tags: e.tags,
	})
	if err != nil {
		return err
	}

	if shareSnapshots {
		for snapshotId, tags := range e.snapshotTags {
			_, err := svc.CreateTags(&ec2.CreateTagsInput{
				Resources: []*string{
					aws.String(snapshotId),
				},
				Tags: tags,
			})
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (e *EC2Image) Properties() types.Properties {
	properties := types.NewProperties()
	for _, tagValue := range e.tags {
		properties.SetTag(tagValue.Key, tagValue.Value)
	}
	properties.Set("ID", e.id)
	properties.Set("AMIName", e.name)
	return properties
}

func (e *EC2Image) String() string {
	return e.id
}

func (e *EC2Image) Date() time.Time {
	return e.date
}

func (e *EC2Image) Match(filter common.Filter) bool {
	resourceValue := e.Properties().Get(filter.Property)
	if filter.Invert {
		return resourceValue != filter.Value
	}
	return resourceValue == filter.Value
}

func (e *EC2Image) AddTags(tags map[string]string, tagSnapshots bool) error {
	var awsTags []*ec2.Tag
	for key, value := range tags {
		awsTags = append(awsTags, &ec2.Tag{
			Key:   aws.String(key),
			Value: aws.String(value),
		})
	}
	_, err := e.svc.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{
			aws.String(e.id),
		},
		Tags: awsTags,
	})
	if err != nil {
		return err
	}

	if tagSnapshots {
		for _, snapshotId := range e.snapshots {
			_, err := e.svc.CreateTags(&ec2.CreateTagsInput{
				Resources: []*string{
					aws.String(snapshotId),
				},
				Tags: awsTags,
			})
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (e *EC2Image) ShareWithAccount(accountId string, shareSnapshots bool) error {
	awsAccountId := aws.String(accountId)
	_, err := e.svc.ModifyImageAttribute(
		&ec2.ModifyImageAttributeInput{
			ImageId: aws.String(e.id),
			LaunchPermission: &ec2.LaunchPermissionModifications{
				Add: []*ec2.LaunchPermission{{UserId: awsAccountId}},
			},
		})
	if err != nil {
		return err
	}

	if shareSnapshots {
		for _, snapshotId := range e.snapshots {
			_, err = e.svc.ModifySnapshotAttribute(
				&ec2.ModifySnapshotAttributeInput{
					SnapshotId: aws.String(snapshotId),
					CreateVolumePermission: &ec2.CreateVolumePermissionModifications{
						Add: []*ec2.CreateVolumePermission{{UserId: awsAccountId}},
					},
				})
			if err != nil {
				return err
			}
		}
	}

	return err
}

func (e *EC2Image) MarshalYAML() (interface{}, error) {
	return fmt.Sprintf("ID=%s, Name=%s, Date=%s", e.id, e.name, e.dateStr), nil
}
