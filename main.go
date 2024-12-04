package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	"gopkg.in/yaml.v3"
)

var (
	client   influxdb2.Client
	writeAPI api.WriteAPIBlocking
)

type Config struct {
	SNMP struct {
		Target    string `yaml:"target"`
		Port      uint16 `yaml:"port"`
		Community string `yaml:"community"`
		Version   string `yaml:"version"`
		Timeout   int    `yaml:"timeout"`
		Retries   int    `yaml:"retries"`
		Repeat    int64  `yaml:"repeatEverySecond"`
	} `yaml:"snmp"`
	OIDs []struct {
		OID string `yaml:"oid"`
	} `yaml:"oids"`
	Tnies []struct {
		Tnie string `yaml:"tne"`
	} `yaml:"toNullIfEmpty"`
}

func loadConfig(filename string) (*Config, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	config := &Config{}
	decoder := yaml.NewDecoder(file)
	err = decoder.Decode(config)
	return config, err
}

func main() {
	config, err := loadConfig("config.yml")
	if err != nil {
		log.Fatalf("Ошибка загрузки конфигурации: %v", err)
	}
	done := make(chan bool, 1)
	fmt.Println("Read config: OK")

	influxDBURL := os.Getenv("INFLUX_DBURL")
	token := os.Getenv("INFLUX_TOKEN")
	org := os.Getenv("INFLUX_ORG")
	bucket := os.Getenv("INFLUX_BUCKET")

	client = influxdb2.NewClient(influxDBURL, token)
	writeAPI = client.WriteAPIBlocking(org, bucket)
	fmt.Println("Influx client started")

	var version gosnmp.SnmpVersion
	switch config.SNMP.Version {
	case "1":
		version = gosnmp.Version1
	case "2c":
		version = gosnmp.Version2c
	default:
		log.Fatalf("Неподдерживаемая версия SNMP: %s", config.SNMP.Version)
	}

	// Настройка подключения SNMP
	params := &gosnmp.GoSNMP{
		Target:    config.SNMP.Target,
		Port:      config.SNMP.Port,
		Community: config.SNMP.Community,
		Version:   version,
		Timeout:   time.Duration(config.SNMP.Timeout) * time.Second,
		Retries:   config.SNMP.Retries,
	}

	// Подключение к SNMP-устройству
	err = params.Connect()
	if err != nil {
		log.Fatalf("Ошибка подключения к SNMP-устройству: %v", err)
	}
	defer params.Conn.Close()

	fmt.Println("SNMP client started")

	go starts(params, config)
	<-done

	fmt.Println("Exiting application.")
}

func starts(params *gosnmp.GoSNMP, config *Config) {
	oidMap, err := parseAndReverseYAML("./mibs/EPPC-MIB.yaml")
	if err != nil {
		fmt.Println(err.Error())
	}

	fmt.Println("MIB map parsed")

	for {
		tm := time.Now()
		toExport := make(map[string]any)

		for _, oid := range config.OIDs {

			variables, walkErr := params.WalkAll(oid.OID)
			tm = time.Now()

			if walkErr != nil {
				if strings.Contains(walkErr.Error(), "request timeout (after") {

					toExport["upsESystemStatus"] = 10 //shutdown
					toExport["upsEBatteryStatus"] = 4 //batteryDepleted

					go PushData(toExport, tm)

					time.Sleep(time.Second * time.Duration(config.SNMP.Repeat))

					break
				}

				log.Printf("Ошибка выполнения SNMP-запроса для %s: %v", oid.OID, walkErr)
				break
			}

			for _, variable := range variables {
				oidAdd := strings.TrimLeft(variable.Name, ".")
				name := oidMap[oidAdd]
				if len(name) == 0 {
					oidAdd = oidAdd[:len(oidAdd)-2]
					name = oidMap[oidAdd]
				}
				if len(name) == 0 {
					name = oidAdd
				}
				//fmt.Println(fmt.Sprintf("%s => %s", name, string(variable.Value.([]byte))))
				switch variable.Type {
				case gosnmp.OctetString:
					toExport[name] = string(variable.Value.([]byte))
				case gosnmp.Integer:
					toExport[name] = gosnmp.ToBigInt(variable.Value).Int64()

				case gosnmp.Counter32, gosnmp.Gauge32, gosnmp.TimeTicks, gosnmp.Counter64:
					toExport[name] = gosnmp.ToBigInt(variable.Value).Uint64()

				case gosnmp.OpaqueFloat:
					toExport[name] = variable.Value.(float32)

				case gosnmp.OpaqueDouble:
					toExport[name] = variable.Value.(float64)

				case gosnmp.IPAddress:
					toExport[name] = variable.Value.(string)

				case gosnmp.Opaque, gosnmp.Null, gosnmp.EndOfMibView, gosnmp.NoSuchInstance, gosnmp.NoSuchObject:
					toExport[name] = "null"

				default:
					bytes, ok := variable.Value.([]byte)
					if ok {
						toExport[name] = hex.EncodeToString(bytes)
					}
					toExport[name] = fmt.Sprintf("unknown: %v", variable.Value)
				}

			}

		}

		for _, v := range config.Tnies {
			if mapVal, ok := toExport[v.Tnie]; ok {
				if mapVal.(int64) <= 0 {
					delete(toExport, v.Tnie)
				}
			}
		}

		go PushData(toExport, tm)

		time.Sleep(time.Second * time.Duration(config.SNMP.Repeat))
	}
}

func PushData(data map[string]any, tm time.Time) {
	p := influxdb2.NewPointWithMeasurement("ups_data")
	p.SetTime(time.Now())

	for key, value := range data {
		p.AddField(key, value)
	}

	if len(p.FieldList()) == 0 {
		return
	}

	if err := writeAPI.WritePoint(context.Background(), p); err != nil {
		log.Printf("Error writing point to InfluxDB: %v", err)
	}

}

func parseAndReverseYAML(filePath string) (map[string]string, error) {
	_, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("error reading YAML file: %v", err)
	}

	yamlFile, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading YAML file: %v", err)
	}

	var data map[string]string
	err = yaml.Unmarshal(yamlFile, &data)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling YAML: %v", err)
	}

	reversedMap := make(map[string]string)
	for key, value := range data {
		reversedMap[value] = key
	}

	return reversedMap, nil
}
