// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package synchronization_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/go-azure-helpers/lang/pointer"
	"github.com/hashicorp/go-azure-helpers/lang/response"
	"github.com/hashicorp/go-azure-sdk/microsoft-graph/common-types/stable"
	"github.com/hashicorp/go-azure-sdk/microsoft-graph/serviceprincipals/stable/synchronizationjob"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-provider-azuread/internal/acceptance"
	"github.com/hashicorp/terraform-provider-azuread/internal/acceptance/check"
	"github.com/hashicorp/terraform-provider-azuread/internal/clients"
	"github.com/hashicorp/terraform-provider-azuread/internal/services/synchronization/parse"
)

type SynchronizationJobResource struct{}

func TestAccSynchronizationJob(t *testing.T) {
	acceptance.RunTestsInSequence(t, map[string]map[string]func(t *testing.T){
		"synchronizationJob": {
			"basic":    testAccSynchronizationJob_basic,
			"disabled": testAccSynchronizationJob_disabled,
		},
	})
}

func testAccSynchronizationJob_basic(t *testing.T) {
	data := acceptance.BuildTestData(t, "azuread_synchronization_job", "test")
	r := SynchronizationJobResource{}

	data.ResourceTest(t, r, []acceptance.TestStep{
		{
			Config: r.basic(data),
			Check: acceptance.ComposeTestCheckFunc(
				check.That(data.ResourceName).ExistsInAzure(r),
				check.That(data.ResourceName).Key("template_id").Exists(),
				check.That(data.ResourceName).Key("enabled").HasValue("true"),
			),
		},
		data.ImportStep(),
	})
}

func testAccSynchronizationJob_disabled(t *testing.T) {
	data := acceptance.BuildTestData(t, "azuread_synchronization_job", "test")
	r := SynchronizationJobResource{}

	data.ResourceTest(t, r, []acceptance.TestStep{
		{
			Config: r.disabled(data),
			Check: acceptance.ComposeTestCheckFunc(
				check.That(data.ResourceName).ExistsInAzure(r),
				check.That(data.ResourceName).Key("template_id").Exists(),
				check.That(data.ResourceName).Key("enabled").HasValue("false"),
			),
		},
		data.ImportStep(),
	})
}

func (r SynchronizationJobResource) Exists(ctx context.Context, clients *clients.Client, state *terraform.InstanceState) (*bool, error) {
	client := clients.ServicePrincipals.SynchronizationJobClient

	resourceId, err := parse.SynchronizationJobID(state.ID)
	if err != nil {
		return nil, fmt.Errorf("parsing synchronization job ID: %v", err)
	}

	id := stable.NewServicePrincipalIdSynchronizationJobID(resourceId.ServicePrincipalId, resourceId.JobId)

	resp, err := client.GetSynchronizationJob(ctx, id, synchronizationjob.DefaultGetSynchronizationJobOperationOptions())
	if err != nil {
		if response.WasNotFound(resp.HttpResponse) {
			return pointer.To(false), nil
		}
		return nil, fmt.Errorf("retrieving %s", id)
	}

	return pointer.To(true), nil
}

func (SynchronizationJobResource) template(data acceptance.TestData) string {
	return fmt.Sprintf(`
provider "azuread" {}

data "azuread_client_config" "test" {}

data "azuread_application_template" "test" {
  display_name = "Azure Databricks SCIM Provisioning Connector"
}

resource "azuread_application" "test" {
  display_name = "acctestSynchronizationJob-%[1]d"
  owners       = [data.azuread_client_config.test.object_id]
  template_id  = data.azuread_application_template.test.template_id
}

resource "azuread_service_principal" "test" {
  application_id = azuread_application.test.application_id
  owners         = [data.azuread_client_config.test.object_id]
  use_existing   = true
}
`, data.RandomInteger)
}

func (r SynchronizationJobResource) basic(data acceptance.TestData) string {
	return fmt.Sprintf(`
%[1]s

resource "azuread_synchronization_job" "test" {
  service_principal_id = azuread_service_principal.test.id
  template_id          = "dataBricks"
}
`, r.template(data))
}

func (r SynchronizationJobResource) disabled(data acceptance.TestData) string {
	return fmt.Sprintf(`
%[1]s

resource "azuread_synchronization_job" "test" {
  service_principal_id = azuread_service_principal.test.id
  template_id          = "dataBricks"
  enabled              = false
}
`, r.template(data))
}
