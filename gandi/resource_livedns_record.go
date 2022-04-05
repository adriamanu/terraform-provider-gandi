package gandi

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-gandi/go-gandi/types"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

const TXT = "TXT"

func resourceLiveDNSRecord() *schema.Resource {
	return &schema.Resource{
		Create: resourceLiveDNSRecordCreate,
		Read:   resourceLiveDNSRecordRead,
		Update: resourceLiveDNSRecordUpdate,
		Delete: resourceLiveDNSRecordDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		Schema: map[string]*schema.Schema{
			"zone": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "The FQDN of the domain",
			},
			"name": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "The name of the record",
			},
			"type": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "The type of the record",
			},
			"ttl": {
				Type:        schema.TypeInt,
				Required:    true,
				Description: "The TTL of the record",
			},
			"href": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "The href of the record",
			},
			"values": {
				Type:        schema.TypeSet,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Required:    true,
				Description: "A list of values of the record",
			},
			"mutable": {
				Type:        schema.TypeBool,
				Optional:    true,
				Description: "Define if the record can be modified outside Terraform",
			},
		},
		Timeouts: &schema.ResourceTimeout{Default: schema.DefaultTimeout(1 * time.Minute)},
	}
}

func expandRecordID(id string) (zone, name, recordType string, err error) {
	splitID := strings.Split(id, "/")

	if len(splitID) != 3 {
		err = errors.New("id format should be '{zone_id}/{record_name}/{record_type}'")
		return
	}

	zone = splitID[0]
	name = splitID[1]
	recordType = splitID[2]
	return
}

func keepUniqueRecords(recordsList []string) []string {
	keys := make(map[string]bool)
	uniqueRecords := []string{}
	for _, entry := range recordsList {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			uniqueRecords = append(uniqueRecords, entry)
		}
	}
	return uniqueRecords
}

func isRecordWrappedWithQuotes(record string) bool {
	return strings.HasPrefix(record, "\"") && strings.HasSuffix(record, "\"")
}

func containsRecord(recordsList []string, recordToFind string) int {
	for i, rec := range recordsList {
		if rec == recordToFind {
			return i
		}
	}
	return -1
}

func removeRecordFromValuesList(records []string, index int) []string {
	return append(records[:index], records[index+1:]...)
}

func createRecord(d *schema.ResourceData, meta interface{}, zoneUUID, name, recordType string, ttl int, values []string) error {
	client := meta.(*clients).LiveDNS

	_, err := client.CreateDomainRecord(zoneUUID, name, recordType, ttl, values)
	if err != nil {
		return err
	}
	calculatedID := fmt.Sprintf("%s/%s/%s", zoneUUID, name, recordType)
	d.SetId(calculatedID)
	return resourceLiveDNSRecordRead(d, meta)
}

func resourceLiveDNSRecordCreate(d *schema.ResourceData, meta interface{}) error {
	zoneUUID := d.Get("zone").(string)
	name := d.Get("name").(string)
	recordType := d.Get("type").(string)
	ttl := d.Get("ttl").(int)
	valuesList := d.Get("values").(*schema.Set).List()
	mutable := d.Get("mutable").(bool)

	var values []string
	for _, v := range valuesList {
		values = append(values, v.(string))
	}
	client := meta.(*clients).LiveDNS

	// Retrieve existing records - create if not exists otherwise update records with new values
	if recordType == TXT && mutable {
		rec, err := client.GetDomainRecordByNameAndType(zoneUUID, name, recordType)
		if err != nil {
			return createRecord(d, meta, zoneUUID, name, recordType, ttl, values)
		}
		values = append(values, rec.RrsetValues...)
		_, err = client.UpdateDomainRecordByNameAndType(zoneUUID, name, recordType, ttl, values)
		if err != nil {
			return err
		}
	}
	return createRecord(d, meta, zoneUUID, name, recordType, ttl, values)
}

func resourceLiveDNSRecordRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients).LiveDNS
	zone, name, recordType, err := expandRecordID(d.Id())
	mutable := d.Get("mutable").(bool)
	if err != nil {
		return err
	}
	record, err := client.GetDomainRecordByNameAndType(zone, name, recordType)

	if err != nil {
		requestError, ok := err.(*types.RequestError)
		if ok && requestError.StatusCode == 404 {
			d.SetId("")
			return nil
		}
		return err
	}

	if err = d.Set("zone", zone); err != nil {
		return fmt.Errorf("failed to set zone for %s: %w", d.Id(), err)
	}
	if err = d.Set("name", record.RrsetName); err != nil {
		return fmt.Errorf("failed to set name for %s: %w", d.Id(), err)
	}
	if err = d.Set("type", record.RrsetType); err != nil {
		return fmt.Errorf("failed to set type for %s: %w", d.Id(), err)
	}
	if err = d.Set("ttl", record.RrsetTTL); err != nil {
		return fmt.Errorf("failed to set ttl for %s: %w", d.Id(), err)
	}
	if err = d.Set("href", record.RrsetHref); err != nil {
		return fmt.Errorf("failed to set href for %s: %w", d.Id(), err)
	}
	if recordType == TXT && mutable {
		// Keep only values defined within terraform rather than list of all records
		if err = d.Set("values", d.Get("values").(*schema.Set).List()); err != nil {
			return fmt.Errorf("failed to set the values for %s: %w", d.Id(), err)
		}
	} else {
		if err = d.Set("values", record.RrsetValues); err != nil {
			return fmt.Errorf("failed to set the values for %s: %w", d.Id(), err)
		}
	}

	return nil
}

func resourceLiveDNSRecordUpdate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients).LiveDNS
	zone, name, recordType, err := expandRecordID(d.Id())
	if err != nil {
		return err
	}

	mutable := d.Get("mutable").(bool)
	ttl := d.Get("ttl").(int)
	valuesList := d.Get("values").(*schema.Set).List()
	var values []string
	for _, v := range valuesList {
		values = append(values, v.(string))
	}

	if recordType == TXT && mutable {
		rec, err := client.GetDomainRecordByNameAndType(zone, name, recordType)
		if err != nil {
			return err
		}
		var recordsWithQuotes []string
		existingAndManagedRecords := append(values, rec.RrsetValues...)
		for i := range existingAndManagedRecords {
			record := fmt.Sprintf("%v", existingAndManagedRecords[i])
			if isRecordWrappedWithQuotes(record) {
				recordsWithQuotes = append(recordsWithQuotes, record)
			} else {
				recordsWithQuotes = append(recordsWithQuotes, "\""+record+"\"")
			}
		}
		values = keepUniqueRecords(recordsWithQuotes)
	}

	_, err = client.UpdateDomainRecordByNameAndType(zone, name, recordType, ttl, values)
	if err != nil {
		return err
	}
	return resourceLiveDNSRecordRead(d, meta)
}

func resourceLiveDNSRecordDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients).LiveDNS
	zone, name, recordType, err := expandRecordID(d.Id())
	mutable := d.Get("mutable").(bool)

	if err != nil {
		return err
	}

	if recordType == TXT && mutable {
		zoneUUID := d.Get("zone").(string)
		valuesList := d.Get("values").(*schema.Set).List()
		ttl := d.Get("ttl").(int)

		rec, err := client.GetDomainRecordByNameAndType(zoneUUID, name, recordType)
		if err != nil {
			return err
		}

		// If the amount of records returned by the API is equal to amount of records handled by terraform
		// It means that all resources are managed by Terraform and then records can be safely deleted
		// Otherwise we need to remove terraform managed records from the records list and update it
		if len(rec.RrsetValues) == len(valuesList) {
			if err = client.DeleteDomainRecord(zone, name, recordType); err != nil {
				return err
			}
		} else {
			var values []string = rec.RrsetValues
			for _, v := range valuesList {
				index := containsRecord(values, "\""+v.(string)+"\"")
				if index != -1 {
					values = removeRecordFromValuesList(values, index)
				}
			}
			_, err = client.UpdateDomainRecordByNameAndType(zoneUUID, name, recordType, ttl, values)
			if err != nil {
				return err
			}
		}
	} else {
		if err = client.DeleteDomainRecord(zone, name, recordType); err != nil {
			return err
		}
	}

	d.SetId("")
	return nil
}
