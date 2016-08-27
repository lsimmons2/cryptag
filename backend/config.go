// Steve Phillips / elimisteve
// 2015.11.04

package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/elimisteve/cryptag"
	"github.com/elimisteve/cryptag/types"
	"github.com/elimisteve/fun"
)

var (
	ErrConfigExists = errors.New("Backend config already exists")
)

type Config struct {
	Name     string
	Type     string // Should be one of: backend.Type*
	New      bool   `json:"-"`
	Key      *[32]byte
	Local    bool
	DataPath string // Used by backend.FileSystem, other local backends

	Custom map[string]interface{} `json:",omitempty"` // Used by Dropbox, Webserver, other backends
}

func (conf *Config) Save(backendsDir string) error {
	overwrite := false
	return conf.save(backendsDir, overwrite)
}

func (conf *Config) Update(backendsDir string) error {
	overwrite := true
	return conf.save(backendsDir, overwrite)
}

func (conf *Config) save(backendsDir string, overwrite bool) error {
	if err := os.MkdirAll(backendsDir, 0700); err != nil && os.IsExist(err) {
		return err
	}

	filename := path.Join(backendsDir, conf.Name+".json")

	if !overwrite {
		if _, err := os.Stat(filename); err == nil {
			log.Printf("Backend config already exists at %v; NOT overwriting",
				filename)
			return ErrConfigExists
		}
	}

	if err := conf.Canonicalize(); err != nil {
		return err
	}
	b, err := json.Marshal(conf)
	if err != nil {
		return err
	}

	if overwrite {
		if err := conf.Backup(backendsDir); err != nil {
			return err
		}
	}

	if err = ioutil.WriteFile(filename, b, 0600); err != nil {
		return err
	}
	log.Printf("Saved backend config: %v\n", filename)

	return nil
}

func (conf *Config) Backup(backendsDir string) error {
	filename := path.Join(backendsDir, conf.Name+".json")

	b, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}

	// Back up old config
	bkup := filename + "-" + cryptag.NowStr()

	if err = ioutil.WriteFile(bkup, b, 0600); err != nil {
		return err
	}

	log.Printf("Backed up %v to %v\n", filename, bkup)

	return nil
}

func (conf *Config) Canonicalize() error {
	if conf.Name == "" {
		return errors.New("Storage backend name cannot be empty")
	}
	conf.Name = strings.TrimSuffix(conf.Name, ".json")

	if fun.ContainsAnyStrings(conf.Name, " ", "\t", "\r", "\n") {
		return fmt.Errorf("Storage backend name `%s` contains one or"+
			" more whitespace characters, shouldn't", conf.Name)
	}

	if conf.Key == nil {
		log.Printf("Generating new encryption key for backend `%s`...",
			conf.Name)
		key, err := cryptag.RandomKey()
		if err != nil {
			return err
		}
		conf.Key = key
	}

	if conf.Local && conf.DataPath == "" {
		conf.DataPath = cryptag.LocalDataPath
	}
	conf.DataPath = strings.TrimRight(conf.DataPath, "/\\")

	return nil
}

func (conf *Config) GetType() string {
	if conf.Type != "" {
		return conf.Type
	}

	if conf.Local && conf.DataPath != "" {
		return TypeFileSystem
	}

	_, ok1 := conf.Custom["AuthToken"]
	_, ok2 := conf.Custom["BaseURL"]

	if ok1 && ok2 {
		return TypeWebserver
	}

	_, ok1 = conf.Custom["AppKey"]
	_, ok2 = conf.Custom["AppSecret"]
	_, ok3 := conf.Custom["AccessToken"]
	_, ok4 := conf.Custom["BasePath"]

	if ok1 && ok2 && ok3 && ok4 {
		return TypeDropboxRemote
	}

	return ""
}

func (conf *Config) GetPath() string {
	typ := conf.GetType()

	switch typ {
	case TypeDropboxRemote:
		return fmt.Sprintf("%s", conf.Custom["BasePath"])
	case TypeFileSystem:
		return conf.DataPath
	case TypeWebserver:
		return fmt.Sprintf("%s", conf.Custom["BaseURL"])
	}

	return ""
}

//
// Convenience Functions
//

func ReadConfig(backendPath, backendName string) (*Config, error) {
	if backendPath == "" {
		backendPath = cryptag.BackendPath
	}
	if backendName == "" {
		return nil, errors.New("backendName cannot be empty")
	}
	backendName = strings.TrimSuffix(backendName, ".json")

	configFile := path.Join(backendPath, backendName+".json")

	if types.Debug {
		log.Printf("Loading backend config file `%v`\n", configFile)
	}

	b, err := ioutil.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	var conf Config

	if err = json.Unmarshal(b, &conf); err != nil {
		return nil, err
	}

	// Ignore 'Name' field in .json file, use filename
	conf.Name = backendName

	return &conf, nil
}

func ReadConfigs(backendPath, bkPattern string) ([]*Config, error) {
	if backendPath == "" {
		backendPath = cryptag.BackendPath
	}

	bkFile := filepath.Join(backendPath, bkPattern+".json")

	backendNames, err := filepath.Glob(bkFile)
	if err != nil {
		return nil, fmt.Errorf("Error globbing Configs with pattern `%s`: %v",
			bkPattern, err)
	}

	configs := make([]*Config, 0, len(backendNames))

	var errs []string

	for _, fname := range backendNames {
		bkName := ConfigNameFromPath(fname)
		conf, err := ReadConfig(backendPath, bkName)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		configs = append(configs, conf)
	}

	if errs != nil {
		return configs, fmt.Errorf("%d errors reading configs: %#v", len(errs),
			errs)
	}

	return configs, nil
}

func ReadBackends(backendPath, bkPattern string) ([]Backend, error) {
	configs, err := ReadConfigs(backendPath, bkPattern)
	if err != nil {
		return nil, err
	}

	backends := make([]Backend, 0, len(configs))

	for _, conf := range configs {
		var bk Backend

		typ := conf.GetType()

		switch typ {
		case TypeDropboxRemote:
			bk, err = DropboxRemoteFromConfig(conf)
		case TypeFileSystem:
			bk, err = NewFileSystem(conf)
		case TypeWebserver:
			bk, err = WebserverFromConfig(conf)
		}

		if err != nil {
			log.Printf("Error creating Backend from Config %s: %v\n", typ, err)
			continue
		}

		backends = append(backends, bk)
	}

	if len(configs) > 0 && len(backends) == 0 {
		// TODO: Abuse of scoping of err; consider making less subtle
		return nil, fmt.Errorf("Error reading config: %v", err)
	}

	return backends, nil
}
