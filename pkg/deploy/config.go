package deploy

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"io/ioutil"
	"reflect"
	"strings"

	"github.com/ghodss/yaml"
)

// NOTICE: when modifying the config definition here, don't forget to update
// DevConfig().

// Config represents configuration object for deployer tooling
type Config struct {
	RPs           []RPConfig     `json:"rps,omitempty"`
	Configuration *Configuration `json:"configuration,omitempty"`
}

// RPConfig represents individual RP configuration
type RPConfig struct {
	Location            string         `json:"location,omitempty"`
	SubscriptionID      string         `json:"subscriptionId,omitempty"`
	RPResourceGroupName string         `json:"rpResourceGroupName,omitempty"`
	Configuration       *Configuration `json:"configuration,omitempty"`
}

// Configuration represents configuration structure
type Configuration struct {
	ACRLocationOverride                *string       `json:"acrLocationOverride,omitempty"`
	ACRResourceID                      *string       `json:"acrResourceId,omitempty" value:"required"`
	RPVersionStorageAccountName        *string       `json:"rpVersionStorageAccountName,omitempty" value:"required"`
	ACRReplicaDisabled                 *bool         `json:"acrReplicaDisabled,omitempty"`
	AdminAPICABundle                   *string       `json:"adminApiCaBundle,omitempty" value:"required"`
	AdminAPIClientCertCommonName       *string       `json:"adminApiClientCertCommonName,omitempty" value:"required"`
	ARMAPICABundle                     *string       `json:"armApiCaBundle,omitempty"`
	ARMAPIClientCertCommonName         *string       `json:"armApiClientCertCommonName,omitempty"`
	ARMClientID                        *string       `json:"armClientId,omitempty"`
	BillingE2EStorageAccountID         *string       `json:"billingE2EStorageAccountId,omitempty"`
	BillingServicePrincipalID          *string       `json:"billingServicePrincipalId,omitempty"`
	ClusterMDSDConfigVersion           *string       `json:"clusterMdsdConfigVersion,omitempty" value:"required"`
	ClusterParentDomainName            *string       `json:"clusterParentDomainName,omitempty" value:"required"`
	DatabaseAccountName                *string       `json:"databaseAccountName,omitempty" value:"required"`
	ExtraClusterKeyvaultAccessPolicies []interface{} `json:"extraClusterKeyvaultAccessPolicies,omitempty" value:"required"`
	ExtraCosmosDBIPs                   []string      `json:"extraCosmosDBIPs,omitempty"`
	ExtraPortalKeyvaultAccessPolicies  []interface{} `json:"extraPortalKeyvaultAccessPolicies,omitempty" value:"required"`
	ExtraServiceKeyvaultAccessPolicies []interface{} `json:"extraServiceKeyvaultAccessPolicies,omitempty" value:"required"`
	FPClientID                         *string       `json:"fpClientId,omitempty" value:"required"`
	FPServerCertCommonName             *string       `json:"fpServerCertCommonName,omitempty"`
	FPServicePrincipalID               *string       `json:"fpServicePrincipalId,omitempty" value:"required"`
	GlobalResourceGroupName            *string       `json:"globalResourceGroupName,omitempty" value:"required"`
	GlobalResourceGroupLocation        *string       `json:"globalResourceGroupLocation,omitempty" value:"required"`
	GlobalSubscriptionID               *string       `json:"globalSubscriptionId,omitempty" value:"required"`
	KeyvaultPrefix                     *string       `json:"keyvaultPrefix,omitempty" value:"required"`
	MDMFrontendURL                     *string       `json:"mdmFrontendUrl,omitempty" value:"required"`
	MDSDEnvironment                    *string       `json:"mdsdEnvironment,omitempty" value:"required"`
	PortalAccessGroupIDs               []string      `json:"portalAccessGroupIds,omitempty" value:"required"`
	PortalClientID                     *string       `json:"portalClientId,omitempty" value:"required"`
	PortalElevatedGroupIDs             []string      `json:"portalElevatedGroupIds,omitempty" value:"required"`
	RPFeatures                         []string      `json:"rpFeatures,omitempty"`
	RPImagePrefix                      *string       `json:"rpImagePrefix,omitempty" value:"required"`
	RPMDSDConfigVersion                *string       `json:"rpMdsdConfigVersion,omitempty" value:"required"`
	RPNSGSourceAddressPrefixes         []string      `json:"rpNsgSourceAddressPrefixes,omitempty" value:"required"`
	RPParentDomainName                 *string       `json:"rpParentDomainName,omitempty" value:"required"`
	SubscriptionResourceGroupName      *string       `json:"subscriptionResourceGroupName,omitempty" value:"required"`
	SubscriptionResourceGroupLocation  *string       `json:"subscriptionResourceGroupLocation,omitempty" value:"required"`
	RPVMSSCapacity                     *int          `json:"rpVmssCapacity,omitempty"`
	SSHPublicKey                       *string       `json:"sshPublicKey,omitempty" value:"required"`
	StorageAccountDomain               *string       `json:"storageAccountDomain,omitempty" value:"required"`
	VMSize                             *string       `json:"vmSize,omitempty" value:"required"`
}

// GetConfig return RP configuration from the file
func GetConfig(path, location string) (*RPConfig, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config *Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	for _, c := range config.RPs {
		if c.Location == location {
			configuration, err := mergeConfig(c.Configuration, config.Configuration)
			if err != nil {
				return nil, err
			}

			c.Configuration = configuration
			return &c, nil
		}
	}

	return nil, fmt.Errorf("location %s not found in %s", location, path)
}

// mergeConfig merges two Configuration structs, replacing each zero field in
// primary with the contents of the corresponding field in secondary
func mergeConfig(primary, secondary *Configuration) (*Configuration, error) {
	sValues := reflect.ValueOf(secondary).Elem()
	pValues := reflect.ValueOf(primary).Elem()

	for i := 0; i < pValues.NumField(); i++ {
		if pValues.Field(i).IsZero() {
			pValues.Field(i).Set(sValues.Field(i))
		}
	}

	return primary, nil
}

// CheckRequiredFields validates configuration whether it provides required fields.
// Config is invalid if required fields are not provided.
func (conf *RPConfig) validate() error {
	configuration := conf.Configuration
	v := reflect.ValueOf(*configuration)
	missingFields := []string{}

	for i := 0; i < v.NumField(); i++ {
		required := v.Type().Field(i).Tag.Get("value") == "required"

		if required && v.Field(i).IsZero() {
			missingFields = append(missingFields, v.Type().Field(i).Name)
		}
	}

	if len(missingFields) == 0 {
		return nil
	}

	return fmt.Errorf("configuration has missing fields: %s", strings.Join(missingFields, ","))
}
