package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"unicode"
)

type config struct {
	Columns             []string `json:"columns"`
	DefaultRegion       string   `json:"default-aws-region"`
	DisableHostKeyCheck *bool    `json:"disable-host-key-check"`
}

type sshKey struct {
	username string
	filename string
}

func (c *config) Merge(other *config) {
	if len(other.Columns) > 0 {
		c.Columns = other.Columns
	}

	if other.DefaultRegion != "" {
		c.DefaultRegion = other.DefaultRegion
	}

	if other.DisableHostKeyCheck != nil {
		c.DisableHostKeyCheck = other.DisableHostKeyCheck
	}
}

type table struct {
	header []string
	rows   [][]string
}

func (t *table) addRow(row []string) {
	t.rows = append(t.rows, row)
}

func (t *table) render() {
	// 1. Calculate number of columns
	nCols := len(t.header)

	for _, r := range t.rows {
		if len(r) > nCols {
			nCols = len(r)
		}
	}

	if len(t.header) != nCols {
		panic("Number of columns in rows does not match header")
	}

	// 2. Calculate column width
	colWidth := make([]int, nCols)

	updateColWidth := func(row []string) {
		for i, col := range row {
			if colWidth[i] < len(col) {
				colWidth[i] = len(col)
			}
		}
	}

	updateColWidth(t.header)

	for _, r := range t.rows {
		updateColWidth(r)
	}

	tableWidth := 1 // left border

	for _, w := range colWidth {
		// 2 for left and right margin, 1 for border
		tableWidth += w + 3
	}

	// 3. Render header
	rowBuf := bytes.NewBuffer(make([]byte, 0, tableWidth))

	const topLeftCorner = '┌'
	const topRightCorner = '┐'
	const leftTee = '├'
	const rightTee = '┤'
	const border = '│'
	const line = '─'
	const bottomLeftCorner = '└'
	const bottomRightCorner = '┘'

	tableLine := strings.Repeat(string(line), tableWidth-2)

	rowBuf.WriteRune(topLeftCorner)
	rowBuf.WriteString(tableLine)
	rowBuf.WriteRune(topRightCorner)
	rowBuf.WriteString("\n")
	os.Stdout.Write(rowBuf.Bytes())

	writeRow := func(row []string) {
		rowBuf.Reset()

		for i, col := range row {
			if i == 0 {
				rowBuf.WriteRune(border)
			}

			rowBuf.WriteByte(' ')

			padding := colWidth[i] - len(col)
			rowBuf.WriteString(col)
			rowBuf.WriteString(strings.Repeat(" ", padding))

			rowBuf.WriteByte(' ')
			rowBuf.WriteRune(border)
		}

		rowBuf.WriteByte('\n')

		os.Stdout.Write(rowBuf.Bytes())
	}

	writeSeparator := func() {
		rowBuf.Reset()
		rowBuf.WriteRune(leftTee)
		rowBuf.WriteString(tableLine)
		rowBuf.WriteRune(rightTee)
		rowBuf.WriteString("\n")
		os.Stdout.Write(rowBuf.Bytes())
	}

	writeRow(t.header)
	writeSeparator()

	for _, r := range t.rows {
		writeRow(r)
	}

	rowBuf.Reset()
	rowBuf.WriteRune(bottomLeftCorner)
	rowBuf.WriteString(tableLine)
	rowBuf.WriteRune(bottomRightCorner)
	rowBuf.WriteString("\n")
	os.Stdout.Write(rowBuf.Bytes())
}

func getConfigDirs() []string {
	dirs := []string{}

	for _, dir := range strings.Split(os.Getenv("XDG_CONFIG_DIRS"), ":") {
		if dir == "" {
			continue
		}

		dirs = append(dirs, dir)
	}

	if user, err := user.Current(); err == nil {
		dirs = append(dirs, path.Join(user.HomeDir, ".config"))
	}

	dirs = append(dirs, "/etc")

	return dirs
}

func loadConfigFromPath(path string) (*config, error) {
	fd, err := os.Open(path)

	if os.IsNotExist(err) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	defer fd.Close()

	conf := &config{}

	if err := json.NewDecoder(fd).Decode(conf); err != nil {
		return nil, err
	}

	return conf, nil
}

func parseKeySpec(spec string) (username string, keyName string, err error) {
	idx := strings.IndexByte(spec, '@')

	if idx == -1 {
		return "", "", fmt.Errorf("Inalid key spec '%s': missing @", spec)
	}

	return spec[:idx], spec[1+idx:], nil
}

func loadSshKeysFromDir(dirPath string) (map[string]*sshKey, error) {
	dir, err := os.Open(dirPath)

	if os.IsNotExist(err) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	defer dir.Close()

	fis, err := dir.Readdir(0)

	if err != nil {
		return nil, err
	}

	keys := map[string]*sshKey{}

	for _, fi := range fis {
		if fi.IsDir() {
			continue
		}

		if !strings.HasSuffix(fi.Name(), ".pem") {
			continue
		}

		keySpec := path.Base(fi.Name())
		keySpec = keySpec[:len(keySpec)-4]

		username, keyName, err := parseKeySpec(keySpec)

		if err != nil {
			return nil, err
		}

		keys[keyName] = &sshKey{
			username: username,
			filename: path.Join(dirPath, fi.Name()),
		}
	}

	return keys, nil
}

func loadConfig() (*config, map[string]*sshKey, error) {
	conf := &config{}
	sshKeys := map[string]*sshKey{}

	loaded := false

	configDirs := getConfigDirs()

	for _, dir := range configDirs {
		newConf, err := loadConfigFromPath(path.Join(dir, "awssh/config.json"))

		if err != nil {
			return nil, nil, err
		}

		if newConf == nil {
			continue
		}

		conf.Merge(newConf)
		loaded = true

		newKeys, err := loadSshKeysFromDir(path.Join(dir, "awssh/keys"))

		if err != nil {
			return nil, nil, err
		}

		for name, key := range newKeys {
			sshKeys[name] = key
		}
	}

	if !loaded {
		return nil, nil, fmt.Errorf("Found no config files in %s", strings.Join(configDirs, ", "))
	}

	return conf, sshKeys, nil
}

func camelCase(name string) string {
	buf := bytes.NewBuffer(nil)

	ucNext := false

	for _, c := range name {
		if c == '-' || c == '_' {
			ucNext = true
			continue
		}

		if ucNext {
			c = unicode.ToUpper(c)
		}

		buf.WriteRune(c)
		ucNext = false
	}

	return buf.String()
}

func collectInstanceData(instance *ec2.Instance) map[string]string {
	val := reflect.Indirect(reflect.ValueOf(instance))
	desc := map[string]string{}

	for i := 0; i < val.NumField(); i++ {
		fieldDef := val.Type().Field(i)
		fieldName := fieldDef.Tag.Get("locationName")

		if fieldName == "" {
			continue
		}

		field := reflect.Indirect(val.Field(i))

		if !field.IsValid() {
			continue
		}

		// Special handling for tags
		if fieldName == "tagSet" {
			tags := field.Interface().([]*ec2.Tag)

			for _, tag := range tags {
				desc["tag:"+*tag.Key] = *tag.Value
			}

			continue
		}

		desc[fieldName] = fmt.Sprintf("%v", field.Interface())
	}

	return desc
}

func getInstances(region string) ([]map[string]string, error) {
	awsec2 := ec2.New(&aws.Config{Region: aws.String(region)})

	res, err := awsec2.DescribeInstances(&ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("instance-state-name"),
				Values: []*string{aws.String(ec2.InstanceStateNameRunning)},
			},
		},
	})

	if err != nil {
		return nil, err
	}

	instances := []map[string]string{}

	for _, reservation := range res.Reservations {
		for _, instance := range reservation.Instances {
			instances = append(instances, collectInstanceData(instance))
		}
	}

	return instances, nil
}

func rowMatchesExact(row []string, exactMatch string) bool {
	for _, col := range row {
		if col == exactMatch {
			return true
		}
	}

	return false
}

func fuzzyMatch(str, match string) bool {
	if len(match) > len(str) {
		return false
	}

	str = strings.ToLower(str)
	match = strings.ToLower(match)

	i := 0
	j := 0

	for {
		if j == len(match) {
			return true
		}

		// We've consumed the whole string without consuming the whole match
		if i == len(str) {
			return false
		}

		// Consume a letter from match if possible
		if match[j] == str[i] {
			j++
		}

		// Always consume a letter from str
		i++
	}
}

func rowMatchesFuzzy(row []string, match string) bool {
	for _, col := range row {
		if fuzzyMatch(col, match) {
			return true
		}
	}

	return false
}

func rowMatches(row []string, fuzzyMatch string, exactMatch string) bool {
	if fuzzyMatch == "" && exactMatch == "" {
		return true
	}

	if exactMatch != "" && rowMatchesExact(row, exactMatch) {
		return true
	}

	if fuzzyMatch != "" && rowMatchesFuzzy(row, fuzzyMatch) {
		return true
	}

	return false
}

func readline() string {
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	return line[:len(line)-1]
}

func getInstanceIP(instance map[string]string) string {
	if ip := instance["ipAddress"]; ip != "" {
		return ip
	}

	if ip := instance["privateIpAddress"]; ip != "" {
		return ip
	}

	panic("Cannot determine IP address for instance " + instance["instanceId"])
}

func main() {
	conf, sshKeys, err := loadConfig()

	if err != nil {
		log.Fatalf("Error while loading configuration: %s", err)
	}

	region := flag.String("r", conf.DefaultRegion, "AWS region to use (set from config if not specified)")
	matchFilter := flag.String("m", "", `Only list instances that have a column matching the filter.
The filtering is fuzzy, a column matches if all letters from the filter appear in the column in that order (eg. "thm" matches "thismatches").`)
	equalFilter := flag.String("e", "", "Only list instances that have a column equals to the given value.")
	flag.Parse()

	if *region == "" {
		log.Fatalf("No region defined, either in the configuration or on the command line")
	}

	instanceTable := &table{}
	instanceTable.header = append([]string{"#"}, conf.Columns...)

	instances, err := getInstances(*region)

	if err != nil {
		log.Fatalf("Error while listing EC2 instances: %s", err)
	}

	// Maps (filtered) instance index to IP address
	instanceIP := map[uint64]string{}
	// Maps (filtered) instance index to key name
	instanceKey := map[uint64]string{}
	instanceIndex := uint64(0)

	for _, instance := range instances {
		row := make([]string, 1+len(conf.Columns))
		row[0] = strconv.FormatUint(uint64(instanceIndex), 10)

		for i, col := range conf.Columns {
			if !strings.HasPrefix(col, "tag:") {
				col = camelCase(col)
			}

			row[1+i] = instance[col]
		}

		if !rowMatches(row[1:], *matchFilter, *equalFilter) {
			continue
		}

		instanceTable.addRow(row)
		instanceIP[instanceIndex] = getInstanceIP(instance)
		instanceKey[instanceIndex] = instance["keyName"]
		instanceIndex++
	}

	var selected uint64

	if len(instanceTable.rows) == 0 {
		fmt.Println("No instances matched the given filters in that region.")
		os.Exit(0)
	} else if len(instanceTable.rows) == 1 {
		selected = 0
	} else {
		instanceTable.render()
		fmt.Print("Instance number: ")

		idxStr := readline()

		if idxStr == "" {
			os.Exit(0)
		}

		var err error
		selected, err = strconv.ParseUint(idxStr, 10, 64)

		if err != nil {
			log.Fatalf("Invalid instance index '%s': %s", idxStr, err)
		}
	}

	if selected >= uint64(len(instanceTable.rows)) {
		log.Fatalf("Invalid instance index %d: too large", selected)
	}

	keyName := instanceKey[selected]
	key := sshKeys[keyName]

	if key == nil {
		fmt.Fprintf(os.Stderr, `
I dont have a key called %s. Please create a file called user@%s.pem in the
keys directory of the AWSSH configuration directory containing the private SSH
key needed to connect to that instance.
`, keyName, keyName)
		os.Exit(1)
	}

	log.Printf("Connecting to %s", instanceIP[selected])

	sshArgs := []string{
		"-t",
		"-i",
		key.filename,
	}

	if conf.DisableHostKeyCheck != nil && *conf.DisableHostKeyCheck {
		sshArgs = append(sshArgs, "-o", "StrictHostKeyChecking no", "-o", "UserKnownHostsFile /dev/null")
	}

	sshArgs = append(sshArgs, key.username+"@"+instanceIP[selected])

	if flag.NArg() > 0 {
		sshArgs = append(sshArgs, strings.Join(flag.Args(), " "))
	}

	sshBin, err := exec.LookPath("ssh")

	if err != nil {
		log.Fatal("Could not find ssh in PATH")
	}

	if err := syscall.Exec(sshBin, sshArgs, nil); err != nil {
		log.Fatalf("Cannot spawn ssh: %s", err)
	}
}
