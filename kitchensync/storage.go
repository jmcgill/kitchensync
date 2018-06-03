package kitchensync

import (
	"github.com/jmoiron/sqlx"
	"log"
	_ "github.com/lib/pq"
	"fmt"
	"strings"
	"io/ioutil"
	"github.com/hashicorp/hcl"
	"path/filepath"
	"os"
	"regexp"
	"database/sql"
)

// I could automatically detect what type of entity a relationship refers to using this reflection query
//SELECT
//	ccu.table_name AS foreign_table_name,
//	ccu.column_name AS foreign_column_name
//FROM
//	information_schema.table_constraints AS tc
//JOIN information_schema.key_column_usage AS kcu
//	ON tc.constraint_name = kcu.constraint_name
//JOIN information_schema.constraint_column_usage AS ccu
//	ON ccu.constraint_name = tc.constraint_name
//WHERE constraint_type = 'FOREIGN KEY' AND tc.table_name='subscriptions_groups' and tc.column_name = 'user_id';

type Config map[string]Table

type Table map[string]Entry

type Entry map[string]interface{}

type SyncEntry struct {
	Tablename string
	Name string
	Id int64
}

type KitchenSync struct {
	db *sqlx.DB
	path string
	config Config
	existing map[string]SyncEntry
}

func NewKitchenSync(path string, dbURI string) (*KitchenSync, error) {
	db, err := sqlx.Connect("postgres", dbURI)
	if err != nil {
		panic("Could not connect to postgres")
	}

	k := &KitchenSync{
		db: db,
		path: path,
	}
	err = k.init()
	if err != nil {
		return nil, err
	}

	return k, nil
}

func NewKitchenSyncWithDb(path string, db *sql.DB, driver string) (*KitchenSync, error) {
	dbx := sqlx.NewDb(db, driver)
	k := &KitchenSync{
		db: dbx,
		path: path,
	}
	err := k.init()
	if err != nil {
		return nil, err
	}

	return k, nil
}

func (k *KitchenSync) expandString(value string) string {
	// Is this a file function?
	re := regexp.MustCompile("\\$file\\(([A-Za-z0-9_/.-]+)\\)")
	if re.MatchString(value) {
		match := re.FindStringSubmatch(value)
		data, err := ioutil.ReadFile(match[1])
		if err != nil {
			data = []byte("")
		}
		return string(data)
	}

	// Escape string
	escaped := strings.Replace(value, "'", "''", -1)
	return escaped
}

func (k *KitchenSync) isReference(value string) (int64, bool, error) {
	// Is this a file function?
	re := regexp.MustCompile("\\${([A-Za-z0-9_.-]+)}")
	if re.MatchString(value) {
		match := re.FindStringSubmatch(value)
		compoundKey := match[1]
		parts := strings.Split(compoundKey, ".")
		table := parts[0]
		name := parts[1]

		if _, ok := k.existing[compoundKey]; !ok {
			err := k.createMissing(table, name, k.config[table][name])
			if err != nil {
				fmt.Printf("Create missing failed with error %v", err)
				return 0, false, err
			}
		}

		fmt.Printf("Returning ID %v", k.existing[compoundKey].Id)
		return k.existing[compoundKey].Id, true, nil
	}
	return 0, false, nil
}

func (k *KitchenSync) collectValues(entries map[string]interface{}, includeDefaults bool) (map[string]string) {
	output := make(map[string]string)

	for key, value := range entries {
		if strings.HasPrefix(key, "_") {
			continue
		}

		switch typedValue := value.(type) {
		case string:
			// TODO(jimmy): Handle the error
			if id, ok, _ := k.isReference(typedValue); ok {
				output[key] = fmt.Sprintf("'%v'", id)
				continue
			}
			output[key] = fmt.Sprintf("'%v'", k.expandString(typedValue))
		default:
			output[key] = fmt.Sprintf("%v", typedValue)
		}
	}

	if (!includeDefaults) {
		return output
	}

	if _, ok := entries["_defaults"]; ok {
		switch defaults := entries["_defaults"].(type) {
		case []map[string]interface{}:
			for key, value := range defaults[0] {
				switch typedValue := value.(type) {
				case string:
					output[key] = fmt.Sprintf("'%v'", k.expandString(typedValue))
				default:
					output[key] = fmt.Sprintf("%v", typedValue)
				}
			}
		default:
			panic("Unexpected value of _defaults")
		}
	}

	return output
}

func (k *KitchenSync) Drop() error {
	log.Print("Dropping all data")

	tables := []string{}
	err := k.db.Select(&tables, "SELECT table_name FROM information_schema.tables WHERE table_schema = 'public';");
	if err != nil {
		return err
	}

	q := fmt.Sprintf("TRUNCATE %v CASCADE", strings.Join(tables, ","))
	_, err = k.db.Exec(q)
	if err != nil {
		return err
	}

	return nil
}

func (k *KitchenSync) Sync(reset bool) error {
	data := make([]byte, 0)
	err := filepath.Walk(k.path, func(path string, info os.FileInfo, err error) error {
		if (filepath.Ext(path) == ".hcl") {
			d, err := ioutil.ReadFile(path)
			if err != nil {
				return err
			}
			data = append(data, d...)
		}
		return nil
	})

	err = hcl.Unmarshal(data, &k.config)
	if err != nil {
		panic(err)
	}

	existingEntries := []SyncEntry{}
	err = k.db.Select(&existingEntries, "SELECT * FROM _kitchensync");
	if err != nil {
		return err
	}

	k.existing = make(map[string]SyncEntry)
	for _, entry := range existingEntries {
		key := fmt.Sprintf("%v.%v", entry.Tablename, entry.Name)
		k.existing[key] = entry
	}

	for table, declared := range k.config {
		for name, d := range declared {
			key := fmt.Sprintf("%v.%v", table, name)

			// Create any entities that aren't registered with Kitchen Sync
			if _, ok := k.existing[key]; !ok {
				err = k.createMissing(table, name, d)
				if err != nil {
					return err
				}
			}
		}

	}

	// Re-create any rows that were registered, but have since been deleted
	for _, row := range k.existing {
		exists, err := k.exists(row.Tablename, row.Id)
		if err != nil {
			return err
		}

		if !exists {
			k.createMissing(row.Tablename, row.Name, k.config[row.Tablename][row.Name])
		}
	}

	// Update all existing rows
	for _, row := range k.existing {
		err = k.updateExisting(row.Tablename, row.Id, k.config[row.Tablename][row.Name], reset)
		if err != nil {
			return err
		}
	}

	return nil
}

func (k *KitchenSync) exists(table string, id int64) (bool, error) {
	log.Printf("%v.%v", table, id)
	result := make([]int, 0)

	q := fmt.Sprintf("SELECT id FROM %v WHERE id = %v", table, id)
	err := k.db.Select(&result, q)
	if err != nil {
		return false, err
	}

	if len(result) == 0 {
		return false, nil
	}
	return true, nil
}

func (k *KitchenSync) createMissing(table string, name string, entry Entry) error {
	// Terminate it already created
	compoundKey := fmt.Sprintf("%v.%v", table, name)
	if _, ok := k.existing[compoundKey]; ok {
		return nil
	}

	keys := make([]string, 0)
	values := make([]string, 0)
	fields := k.collectValues(entry, true)
	for key, value := range fields {
		keys = append(keys, key)
		values = append(values, value)
	}

	q := fmt.Sprintf("INSERT INTO %v (%v) VALUES (%v) RETURNING id", table, strings.Join(keys, ","), strings.Join(values, ","))
	log.Printf("Executing query %v", q)
	rows, err := k.db.Query(q)
	if err != nil {
		return err
	}

	var id int64
	if rows.Next() {
		rows.Scan(&id)
	}

	_, err = k.db.Exec("INSERT INTO _kitchensync (tablename, name, id) VALUES ($1, $2, $3)", table, name, id)
	if err != nil {
		return err
	}

	k.existing[compoundKey] = SyncEntry{
		Tablename: table,
		Name: name,
		Id: id,
	}

	return nil
}

func (k *KitchenSync) updateExisting(table string, id int64, entry Entry, reset bool) error {
	updates := make([]string, 0)
	fields := k.collectValues(entry, reset)
	for key, value := range fields {
		update := fmt.Sprintf("%v = %v", key, value)
		updates = append(updates, update)
	}

	q := fmt.Sprintf("UPDATE %v SET %v WHERE id = %v", table, strings.Join(updates, ", "), id)
	log.Printf("Executing query %v", q)
	_, err := k.db.Exec(q)
	if err != nil {
		return err
	}

	return nil
}

func (k *KitchenSync) init() error {
	_, err := k.db.Exec(`
		CREATE TABLE IF NOT EXISTS _kitchensync (
			tablename TEXT NOT NULL,
			name TEXT NOT NULL,
			id BIGINT NOT NULL,
			PRIMARY KEY(tablename, name)
		)`)
	if err != nil {
		return err
	}

	return nil
}