package nasaclient

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dns-gh/freeze"
	"github.com/dns-gh/tojson"
)

const (
	nasaAsteroidsAPIGet             = "https://api.nasa.gov/neo/rest/v1/feed?api_key="
	nasaAPIDefaultKey               = "DEMO_KEY"
	nasaTimeFormat                  = "2006-01-02"
	fetchMaxSizeError               = "cannot fetch infos for more than 7 days in one request"
	maxRandTimeSleepBetweenRequests = 120 // seconds
)

var (
	asteroidsQualificativeAdjective = []string{
		"harmless",
		"nasty",
		"threatening",
		"dangerous",
		"critical",
		"terrible",
		"bloody",
		"destructive",
		"deadly",
		"fatal",
	}
)

// NasaClient represents the web Client.
type NasaClient struct {
	apiKey      string
	firstOffset int
	offset      int
	poll        time.Duration
	path        string
	body        string // orbiting body to watch
	debug       bool
}

func (n *NasaClient) hasDefaultKey() bool {
	return n.apiKey == nasaAPIDefaultKey
}

// GetPoll returns the polling frequency of the nasa client to the nasa API.
func (n *NasaClient) GetPoll() time.Duration {
	return n.poll
}

// MakeNasaClient creates a web client to make http request
// to the Neo Nasa API: https://api.nasa.gov/api.html#NeoWS
func MakeNasaClient(firstOffset, offset int, poll time.Duration, path, body string, debug bool) *NasaClient {
	log.Println("[nasa] making nasa client")
	apiKey := os.Getenv("NASA_API_KEY")
	if len(apiKey) == 0 {
		apiKey = nasaAPIDefaultKey
	}
	return &NasaClient{
		apiKey:      apiKey,
		firstOffset: firstOffset,
		offset:      offset,
		poll:        poll,
		path:        path,
		body:        body,
		debug:       debug,
	}
}

type links struct {
	Next string `json:"next"`
	Prev string `json:"prev"`
	Self string `json:"self"`
}

type diameter struct {
	EstimatedDiameterMin float64 `json:"estimated_diameter_min"`
	EstimatedDiameterMax float64 `json:"estimated_diameter_max"`
}

type estimatedDiameter struct {
	Kilometers diameter `json:"kilometers"`
	Meters     diameter `json:"meters"`
	Miles      diameter `json:"miles"`
	Feet       diameter `json:"feet"`
}

type relativeVelocity struct {
	KilometersPerSecond string `json:"kilometers_per_second"`
	KilometersPerHour   string `json:"kilometers_per_hour"`
	MilesPerHour        string `json:"miles_per_hour"`
}

type missDistance struct {
	Astronomical string `json:"astronomical"`
	Lunar        string `json:"lunar"`
	Kilometers   string `json:"kilometers"`
	Miles        string `json:"miles"`
}

type closeApprochInfo struct {
	CloseApproachDate      string           `json:"close_approach_date"`
	EpochDateCloseApproach int64            `json:"epoch_date_close_approach"`
	RelativeVelocity       relativeVelocity `json:"relative_velocity"`
	MissDistance           missDistance     `json:"miss_distance"`
	OrbitingBody           string           `json:"orbiting_body"`
}

type object struct {
	Links                          links              `json:"links"`
	NeoReferenceID                 string             `json:"neo_reference_id"`
	Name                           string             `json:"name"`
	NasaJplURL                     string             `json:"nasa_jpl_url"`
	AbsoluteMagnitudeH             float64            `json:"absolute_magnitude_h"`
	EstimatedDiameter              estimatedDiameter  `json:"estimated_diameter"`
	IsPotentiallyHazardousAsteroid bool               `json:"is_potentially_hazardous_asteroid"`
	CloseApproachData              []closeApprochInfo `json:"close_approach_data"`
}

// SpaceRocks (asteroids) represents all asteroids data available between two dates.
// The information is stored in the NearEarthObjects map.
// [Generated with the help of https://mholt.github.io/json-to-go/]
type SpaceRocks struct {
	Links        links `json:"links"`
	ElementCount int   `json:"element_count"`
	// the key of the NearEarthObjects map represents a date with the following format YYYY-MM-DD
	NearEarthObjects map[string][]object `json:"near_earth_objects"`
}

func (n *NasaClient) load() ([]object, error) {
	objects := &[]object{}
	if _, err := os.Stat(n.path); os.IsNotExist(err) {
		tojson.Save(n.path, objects)
	}
	err := tojson.Load(n.path, objects)
	if err != nil {
		return nil, err
	}
	return *objects, nil
}

func merge(previous, current []object) ([]object, []object) {
	merged := []object{}
	diff := []object{}
	added := map[string]struct{}{}
	for _, v := range previous {
		added[v.NeoReferenceID] = struct{}{}
		merged = append(merged, v)
	}
	for _, v := range current {
		if _, ok := added[v.NeoReferenceID]; ok {
			continue
		}
		added[v.NeoReferenceID] = struct{}{}
		merged = append(merged, v)
		diff = append(diff, v)
	}
	return merged, diff
}

func (n *NasaClient) update(current []object) ([]object, error) {
	previous, err := n.load()
	if err != nil {
		return nil, err
	}
	merged, diff := merge(previous, current)
	tojson.Save(n.path, merged)
	return diff, nil
}

func (n *NasaClient) fetchRocks(days int) (*SpaceRocks, error) {
	if days > 7 {
		return nil, fmt.Errorf(fetchMaxSizeError)
	} else if days < -7 {
		return nil, fmt.Errorf(fetchMaxSizeError)
	}
	now := time.Now()
	start := ""
	end := ""
	if days >= 0 {
		start = now.Format(nasaTimeFormat)
		end = now.AddDate(0, 0, days).Format(nasaTimeFormat)
	} else {
		start = now.AddDate(0, 0, days).Format(nasaTimeFormat)
		end = now.Format(nasaTimeFormat)
	}
	url := nasaAsteroidsAPIGet +
		n.apiKey +
		"&start_date=" + start +
		"&end_date=" + end
	resp, err := http.Get(url)
	if err != nil {
		log.Fatalln(err.Error())
	}
	defer resp.Body.Close()
	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if strings.Contains(string(bytes), "OVER_RATE_LIMIT") {
		return nil, fmt.Errorf("http get rate limit reached, wait orz use a proper key instead of the default one")
	}

	spacerocks := &SpaceRocks{}
	json.Unmarshal(bytes, spacerocks)
	return spacerocks, nil
}

func parseTime(value string, timeFormat string) time.Time {
	parsed, err := time.Parse(timeFormat, value)
	if err != nil {
		log.Fatalln(err)
	}
	return parsed
}

func sort(tab []int64, left int, right int) {
	if left >= right {
		return
	}
	pivot := tab[left]
	i := left + 1
	for j := left; j <= right; j++ {
		if pivot > tab[j] {
			tab[i], tab[j] = tab[j], tab[i]
			i++
		}
	}
	tab[left], tab[i-1] = tab[i-1], pivot
	sort(tab, left, i-2)
	sort(tab, i, right)
}

func quickSort(values []int64) {
	sort(values, 0, len(values)-1)
}

func (n *NasaClient) getDangerousRocks(offset int) ([]object, error) {
	rocks, err := n.fetchRocks(offset)
	if err != nil {
		return nil, err
	}
	dangerousByTimestamp := map[int64][]object{}
	keys := []int64{}
	for _, v := range rocks.NearEarthObjects {
		if len(v) != 0 {
			for _, object := range v {
				if object.IsPotentiallyHazardousAsteroid {
					if len(object.CloseApproachData) != 0 &&
						object.CloseApproachData[0].OrbitingBody == n.body {
						t := parseTime(object.CloseApproachData[0].CloseApproachDate, nasaTimeFormat)
						timestamp := t.UnixNano()
						if len(dangerousByTimestamp[timestamp]) == 0 {
							keys = append(keys, timestamp)
						}
						dangerousByTimestamp[timestamp] = append(dangerousByTimestamp[timestamp], object)
					}
				}
			}
		}
	}
	quickSort(keys)
	objects := []object{}
	for _, key := range keys {
		for _, object := range dangerousByTimestamp[key] {
			objects = append(objects, object)
		}
	}
	return objects, nil
}

func (n *NasaClient) sleep() {
	if !n.debug {
		freeze.Sleep(maxRandTimeSleepBetweenRequests)
	}
}

func match(s string) string {
	i := strings.Index(s, "(")
	if i >= 0 {
		temp := s[i:]
		j := strings.Index(temp, ")")
		if j >= 0 {
			return temp[1:j]
		}
	}
	return ""
}

func (n *NasaClient) fetchData(offset int) ([]string, error) {
	log.Println("[nasa] checking nasa rocks...")
	current, err := n.getDangerousRocks(offset)
	if err != nil {
		return nil, err
	}
	log.Println("[nasa] found", len(current), "potential dangerous rocks")
	// TODO only merge and save asteroids once they are tweeted ?
	diff, err := n.update(current)
	if err != nil {
		return nil, err
	}
	formatedDiff := []string{}
	for _, object := range diff {
		n.sleep()
		closeData := object.CloseApproachData[0]
		approachDate := parseTime(closeData.CloseApproachDate, nasaTimeFormat)
		// extract lisible name
		name := match(object.Name)
		if len(name) == 0 {
			name = object.Name
		}
		// extract lisible speed
		speed := closeData.RelativeVelocity.KilometersPerSecond
		parts := strings.Split(speed, ".")
		if len(parts) == 2 && len(parts[1]) > 2 {
			speed = parts[0] + "." + parts[1][0:1]
		}
		// extract lisible month
		month := approachDate.Month().String()
		if len(month) >= 3 {
			month = month[0:3]
		}
		// build status message
		statusMsg := fmt.Sprintf("🔭 a #%s #asteroid %s, Ø ~%.2f km and ~%s km/s is coming close to #%s on %s. %02d (details here %s)",
			freeze.GetRandomElement(asteroidsQualificativeAdjective),
			name,
			(object.EstimatedDiameter.Kilometers.EstimatedDiameterMin+object.EstimatedDiameter.Kilometers.EstimatedDiameterMax)/2,
			speed,
			n.body,
			month,
			approachDate.Day(),
			object.NasaJplURL)
		formatedDiff = append(formatedDiff, statusMsg)
	}
	return formatedDiff, nil
}

// FirstFetch fetches NEO Nasa information with the first offset
func (n *NasaClient) FirstFetch() ([]string, error) {
	return n.fetchData(n.firstOffset)
}

// Fetch fetches NEO Nasa information with default offset
func (n *NasaClient) Fetch() ([]string, error) {
	return n.fetchData(n.offset)
}
