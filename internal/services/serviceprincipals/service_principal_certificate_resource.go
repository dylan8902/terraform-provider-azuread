// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package serviceprincipals

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hashicorp/go-azure-helpers/lang/pointer"
	"github.com/hashicorp/go-azure-helpers/lang/response"
	"github.com/hashicorp/go-azure-sdk/microsoft-graph/common-types/stable"
	"github.com/hashicorp/go-azure-sdk/microsoft-graph/serviceprincipals/stable/serviceprincipal"
	"github.com/hashicorp/terraform-provider-azuread/internal/clients"
	"github.com/hashicorp/terraform-provider-azuread/internal/helpers/consistency"
	"github.com/hashicorp/terraform-provider-azuread/internal/helpers/credentials"
	"github.com/hashicorp/terraform-provider-azuread/internal/helpers/tf"
	"github.com/hashicorp/terraform-provider-azuread/internal/helpers/tf/pluginsdk"
	"github.com/hashicorp/terraform-provider-azuread/internal/helpers/tf/validation"
	"github.com/hashicorp/terraform-provider-azuread/internal/services/serviceprincipals/parse"
)

func servicePrincipalCertificateResource() *pluginsdk.Resource {
	return &pluginsdk.Resource{
		CreateContext: servicePrincipalCertificateResourceCreate,
		ReadContext:   servicePrincipalCertificateResourceRead,
		DeleteContext: servicePrincipalCertificateResourceDelete,

		Timeouts: &pluginsdk.ResourceTimeout{
			Create: pluginsdk.DefaultTimeout(5 * time.Minute),
			Read:   pluginsdk.DefaultTimeout(5 * time.Minute),
			Delete: pluginsdk.DefaultTimeout(5 * time.Minute),
		},

		Importer: pluginsdk.ImporterValidatingResourceId(func(id string) error {
			_, err := parse.CertificateID(id)
			return err
		}),

		Schema: map[string]*pluginsdk.Schema{
			"service_principal_id": {
				Description:      "The object ID of the service principal for which this certificate should be created",
				Type:             pluginsdk.TypeString,
				Required:         true,
				ForceNew:         true,
				ValidateDiagFunc: validation.ValidateDiag(validation.IsUUID),
			},

			"key_id": {
				Description:      "A UUID used to uniquely identify this certificate. If not specified a UUID will be automatically generated",
				Type:             pluginsdk.TypeString,
				Optional:         true,
				Computed:         true,
				ForceNew:         true,
				ValidateDiagFunc: validation.ValidateDiag(validation.IsUUID),
			},

			"encoding": {
				Description: "Specifies the encoding used for the supplied certificate data",
				Type:        pluginsdk.TypeString,
				Optional:    true,
				ForceNew:    true,
				Default:     "pem",
				ValidateFunc: validation.StringInSlice([]string{
					"base64",
					"hex",
					"pem",
				}, false),
			},

			"start_date": {
				Description:  "The start date from which the certificate is valid, formatted as an RFC3339 date string (e.g. `2018-01-01T01:02:03Z`). If this isn't specified, the current date is used",
				Type:         pluginsdk.TypeString,
				Optional:     true,
				Computed:     true,
				ForceNew:     true,
				ValidateFunc: validation.IsRFC3339Time,
			},

			"end_date": {
				Description:   "The end date until which the certificate is valid, formatted as an RFC3339 date string (e.g. `2018-01-01T01:02:03Z`)",
				Type:          pluginsdk.TypeString,
				Optional:      true,
				Computed:      true,
				ForceNew:      true,
				ConflictsWith: []string{"end_date_relative"},
				ValidateFunc:  validation.IsRFC3339Time,
			},

			"end_date_relative": {
				Description:      "A relative duration for which the certificate is valid until, for example `240h` (10 days) or `2400h30m`. Valid time units are \"ns\", \"us\" (or \"µs\"), \"ms\", \"s\", \"m\", \"h\"",
				Type:             pluginsdk.TypeString,
				Optional:         true,
				ForceNew:         true,
				ConflictsWith:    []string{"end_date"},
				ValidateDiagFunc: validation.ValidateDiag(validation.StringIsNotEmpty),
			},

			"type": {
				Description:  "The type of key/certificate",
				Type:         pluginsdk.TypeString,
				Optional:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringInSlice(possibleValuesForKeyCredentialType, false),
			},

			"value": {
				Description: "The certificate data, which can be PEM encoded, base64 encoded DER or hexadecimal encoded DER",
				Type:        pluginsdk.TypeString,
				Required:    true,
				ForceNew:    true,
				Sensitive:   true,
			},
		},
	}
}

func servicePrincipalCertificateResourceCreate(ctx context.Context, d *pluginsdk.ResourceData, meta interface{}) pluginsdk.Diagnostics {
	client := meta.(*clients.Client).ServicePrincipals.ServicePrincipalClient
	objectId := d.Get("service_principal_id").(string)

	credential, err := credentials.KeyCredentialForResource(d)
	if err != nil {
		attr := ""
		if kerr, ok := err.(credentials.CredentialError); ok {
			attr = kerr.Attr()
		}
		return tf.ErrorDiagPathF(err, attr, "Generating certificate credentials for service principal with object ID %q", objectId)
	}

	if credential.KeyId == nil {
		return tf.ErrorDiagF(errors.New("keyId for certificate credential is nil"), "Creating certificate credential")
	}

	id := parse.NewCredentialID(objectId, "certificate", credential.KeyId.GetOrZero())
	servicePrincipalId := stable.NewServicePrincipalID(id.ObjectId)

	tf.LockByName(servicePrincipalResourceName, id.ObjectId)
	defer tf.UnlockByName(servicePrincipalResourceName, id.ObjectId)

	resp, err := client.GetServicePrincipal(ctx, servicePrincipalId, serviceprincipal.DefaultGetServicePrincipalOperationOptions())
	if err != nil {
		if response.WasNotFound(resp.HttpResponse) {
			return tf.ErrorDiagPathF(nil, "service_principal_id", "%s was not found", servicePrincipalId)
		}
		return tf.ErrorDiagPathF(err, "service_principal_id", "Retrieving %s", servicePrincipalId)
	}

	servicePrincipal := resp.Model
	if servicePrincipal == nil {
		return tf.ErrorDiagF(errors.New("model was nil"), "Retrieving %s", servicePrincipalId)
	}

	newCredentials := make([]stable.KeyCredential, 0)
	if servicePrincipal.KeyCredentials != nil {
		for _, cred := range *servicePrincipal.KeyCredentials {
			if strings.EqualFold(cred.KeyId.GetOrZero(), credential.KeyId.GetOrZero()) {
				return tf.ImportAsExistsDiag("azuread_service_principal_certificate", id.String())
			}
			newCredentials = append(newCredentials, cred)
		}
	}

	newCredentials = append(newCredentials, *credential)

	properties := stable.ServicePrincipal{
		KeyCredentials: &newCredentials,
	}
	if _, err = client.UpdateServicePrincipal(ctx, servicePrincipalId, properties); err != nil {
		return tf.ErrorDiagF(err, "Adding certificate for %s", servicePrincipalId)
	}

	// Wait for the credential to appear in the service principal manifest, this can take several minutes
	timeout, _ := ctx.Deadline()
	polledForCredential, err := (&pluginsdk.StateChangeConf{ //nolint:staticcheck
		Pending:                   []string{"Waiting"},
		Target:                    []string{"Done"},
		Timeout:                   time.Until(timeout),
		MinTimeout:                1 * time.Second,
		ContinuousTargetOccurence: 5,
		Refresh: func() (interface{}, string, error) {
			if _, err := client.GetServicePrincipal(ctx, servicePrincipalId, serviceprincipal.DefaultGetServicePrincipalOperationOptions()); err != nil {
				return nil, "Error", err
			}

			if servicePrincipal.KeyCredentials != nil {
				for _, cred := range *servicePrincipal.KeyCredentials {
					if strings.EqualFold(cred.KeyId.GetOrZero(), id.KeyId) {
						return &cred, "Done", nil
					}
				}
			}

			return nil, "Waiting", nil
		},
	}).WaitForStateContext(ctx)

	if err != nil {
		return tf.ErrorDiagF(err, "Waiting for certificate credential for %s", servicePrincipalId)
	} else if polledForCredential == nil {
		return tf.ErrorDiagF(errors.New("certificate credential not found in service principal manifest"), "Waiting for certificate credential for %s", servicePrincipalId)
	}

	d.SetId(id.String())

	return servicePrincipalCertificateResourceRead(ctx, d, meta)
}

func servicePrincipalCertificateResourceRead(ctx context.Context, d *pluginsdk.ResourceData, meta interface{}) pluginsdk.Diagnostics {
	client := meta.(*clients.Client).ServicePrincipals.ServicePrincipalClient

	id, err := parse.CertificateID(d.Id())
	if err != nil {
		return tf.ErrorDiagPathF(err, "id", "Parsing certificate credential with ID %q", d.Id())
	}

	servicePrincipalId := stable.NewServicePrincipalID(id.ObjectId)

	resp, err := client.GetServicePrincipal(ctx, servicePrincipalId, serviceprincipal.DefaultGetServicePrincipalOperationOptions())
	if err != nil {
		if response.WasNotFound(resp.HttpResponse) {
			log.Printf("[DEBUG] Service Principal with ID %q for %s credential %q was not found - removing from state!", id.ObjectId, id.KeyType, id.KeyId)
			d.SetId("")
			return nil
		}
		return tf.ErrorDiagPathF(err, "service_principal_id", "Retrieving service principal with object ID %q", id.ObjectId)
	}

	servicePrincipal := resp.Model
	if servicePrincipal == nil {
		return tf.ErrorDiagF(err, "Retrieving %s", servicePrincipalId)
	}

	credential := credentials.GetKeyCredential(servicePrincipal.KeyCredentials, id.KeyId)
	if credential == nil {
		log.Printf("[DEBUG] Certificate credential %q (ID %q) was not found - removing from state!", id.KeyId, id.ObjectId)
		d.SetId("")
		return nil
	}

	tf.Set(d, "service_principal_id", id.ObjectId)
	tf.Set(d, "key_id", id.KeyId)
	tf.Set(d, "type", credential.Type.GetOrZero())
	tf.Set(d, "start_date", credential.StartDateTime.GetOrZero())
	tf.Set(d, "end_date", credential.EndDateTime.GetOrZero())

	return nil
}

func servicePrincipalCertificateResourceDelete(ctx context.Context, d *pluginsdk.ResourceData, meta interface{}) pluginsdk.Diagnostics {
	client := meta.(*clients.Client).ServicePrincipals.ServicePrincipalClient

	id, err := parse.CertificateID(d.Id())
	if err != nil {
		return tf.ErrorDiagPathF(err, "id", "Parsing certificate credential with ID %q", d.Id())
	}

	tf.LockByName(servicePrincipalResourceName, id.ObjectId)
	defer tf.UnlockByName(servicePrincipalResourceName, id.ObjectId)

	servicePrincipalId := stable.NewServicePrincipalID(id.ObjectId)

	resp, err := client.GetServicePrincipal(ctx, servicePrincipalId, serviceprincipal.DefaultGetServicePrincipalOperationOptions())
	if err != nil {
		if response.WasNotFound(resp.HttpResponse) {
			return tf.ErrorDiagPathF(fmt.Errorf("Service Principal was not found"), "service_principal_id", "Retrieving %s", servicePrincipalId)
		}
		return tf.ErrorDiagPathF(err, "service_principal_id", "Retrieving %s", servicePrincipalId)
	}

	servicePrincipal := resp.Model
	if servicePrincipal == nil {
		return tf.ErrorDiagF(err, "Retrieving %s", servicePrincipalId)
	}

	newCredentials := make([]stable.KeyCredential, 0)
	if servicePrincipal.KeyCredentials != nil {
		for _, cred := range *servicePrincipal.KeyCredentials {
			if !strings.EqualFold(cred.KeyId.GetOrZero(), id.KeyId) {
				newCredentials = append(newCredentials, cred)
			}
		}
	}

	properties := stable.ServicePrincipal{
		KeyCredentials: &newCredentials,
	}
	if _, err := client.UpdateServicePrincipal(ctx, servicePrincipalId, properties); err != nil {
		return tf.ErrorDiagF(err, "Removing certificate credential %q from %s", id.KeyId, servicePrincipalId)
	}

	// Wait for service principal certificate to be deleted
	if err := consistency.WaitForDeletion(ctx, func(ctx context.Context) (*bool, error) {
		if _, err := client.GetServicePrincipal(ctx, servicePrincipalId, serviceprincipal.DefaultGetServicePrincipalOperationOptions()); err != nil {
			return nil, err
		}

		credential := credentials.GetKeyCredential(servicePrincipal.KeyCredentials, id.KeyId)
		if credential == nil {
			return pointer.To(false), nil
		}

		return pointer.To(true), nil
	}); err != nil {
		return tf.ErrorDiagF(err, "Waiting for deletion of certificate credential %q from %s", id.KeyId, servicePrincipalId)
	}

	return nil
}
