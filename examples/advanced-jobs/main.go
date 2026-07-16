package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/scotthaleen/go-app"
	"github.com/scotthaleen/go-toolbelt/echoserver"
	"github.com/scotthaleen/go-toolbelt/logging"
)

type JobStatus string

const (
	JobRunning   JobStatus = "running"
	JobCompleted JobStatus = "completed"
	JobCanceled  JobStatus = "canceled"
)

type Job struct {
	ID        int        `json:"id"`
	Duration  string     `json:"duration"`
	Status    JobStatus  `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

type JobManager struct {
	mu      sync.Mutex
	nextID  int
	jobs    map[int]*Job
	cancels map[int]context.CancelFunc
	wg      sync.WaitGroup
}

func NewJobManager() *JobManager {
	return &JobManager{
		jobs:    make(map[int]*Job),
		cancels: make(map[int]context.CancelFunc),
	}
}

func (m *JobManager) Component() *app.Component {
	return app.NewComponent(
		app.WithName("job manager"),
		app.WithOnStart(m.Start),
		app.WithOnStop(m.Stop),
	)
}

func (m *JobManager) Start(ctx context.Context) error {
	return nil
}

func (m *JobManager) Stop(ctx context.Context) error {
	m.mu.Lock()
	for _, cancel := range m.cancels {
		cancel()
	}
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *JobManager) StartJob(parent context.Context, duration time.Duration) Job {
	m.mu.Lock()
	m.nextID++
	id := m.nextID
	ctx, cancel := context.WithCancel(parent)
	job := &Job{
		ID:        id,
		Duration:  duration.String(),
		Status:    JobRunning,
		CreatedAt: time.Now(),
	}
	m.jobs[id] = job
	m.cancels[id] = cancel
	m.wg.Add(1)
	m.mu.Unlock()

	go m.run(ctx, id, duration)
	return *job
}

func (m *JobManager) CancelJob(id int) bool {
	m.mu.Lock()
	cancel, ok := m.cancels[id]
	m.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

func (m *JobManager) Jobs() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()

	jobs := make([]Job, 0, len(m.jobs))
	for _, job := range m.jobs {
		jobs = append(jobs, *job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	return jobs
}

func (m *JobManager) run(ctx context.Context, id int, duration time.Duration) {
	defer m.wg.Done()
	status := JobCompleted
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-ctx.Done():
		status = JobCanceled
	}

	m.mu.Lock()
	if job := m.jobs[id]; job != nil {
		endedAt := time.Now()
		job.Status = status
		job.EndedAt = &endedAt
	}
	delete(m.cancels, id)
	m.mu.Unlock()
}

type JobHTTP struct {
	jobs *JobManager
}

func NewJobHTTP() *JobHTTP {
	return &JobHTTP{}
}

func (h *JobHTTP) Component() *app.Component {
	return app.NewComponent(
		app.WithName("job http adapter"),
		app.WithOnStart(h.Start),
	)
}

func (h *JobHTTP) Start(ctx context.Context) error {
	router := app.MustGet[*echoserver.Router](ctx)
	h.jobs = app.MustGet[*JobManager](ctx)
	runtime := app.MustGet[app.RuntimeContext](ctx)
	requestShutdown := app.MustGet[app.RequestShutdownFunc](ctx)

	api := router.Group("/api")
	api.GET("/jobs", h.list)
	api.POST("/jobs", h.start(runtime))
	api.DELETE("/jobs/:id", h.cancel)
	router.Echo().GET("/health", func(c echo.Context) error { return c.NoContent(http.StatusOK) })
	router.Echo().POST("/shutdown", h.shutdown(requestShutdown))

	return nil
}

func (h *JobHTTP) list(c echo.Context) error {
	return c.JSON(http.StatusOK, h.jobs.Jobs())
}

func (h *JobHTTP) start(ctx context.Context) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req struct {
			Duration string `json:"duration"`
		}
		if err := c.Bind(&req); err != nil {
			return err
		}
		duration := 10 * time.Second
		if strings.TrimSpace(req.Duration) != "" {
			parsed, err := time.ParseDuration(req.Duration)
			if err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, "invalid duration")
			}
			duration = parsed
		}
		return c.JSON(http.StatusCreated, h.jobs.StartJob(ctx, duration))
	}
}

func (h *JobHTTP) cancel(c echo.Context) error {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid job id")
	}
	if !h.jobs.CancelJob(id) {
		return echo.NewHTTPError(http.StatusNotFound, "job not running")
	}
	return c.NoContent(http.StatusAccepted)
}

func (h *JobHTTP) shutdown(requestShutdown app.RequestShutdownFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		go requestShutdown()
		return c.JSON(http.StatusAccepted, map[string]string{"status": "shutting down"})
	}
}

type JobCLI struct {
	in   io.Reader
	out  io.Writer
	jobs *JobManager
}

func NewJobCLI(in io.Reader, out io.Writer) *JobCLI {
	return &JobCLI{in: in, out: out}
}

func (c *JobCLI) Component() *app.Component {
	return app.NewComponent(
		app.WithName("job cli adapter"),
		app.WithOnStart(c.Start),
	)
}

func (c *JobCLI) Start(ctx context.Context) error {
	c.jobs = app.MustGet[*JobManager](ctx)
	runtime := app.MustGet[app.RuntimeContext](ctx)
	requestShutdown := app.MustGet[app.RequestShutdownFunc](ctx)
	if c.in == nil {
		c.in = os.Stdin
	}
	if c.out == nil {
		c.out = os.Stdout
	}

	fmt.Fprintln(c.out, "job cli: start 5s | list | cancel <id> | quit")
	go c.run(runtime, requestShutdown)
	return nil
}

func (c *JobCLI) run(ctx context.Context, requestShutdown app.RequestShutdownFunc) {
	lines, errs := scanLines(c.in)
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errs:
			if err != nil {
				fmt.Fprintf(c.out, "read input: %v\n", err)
			}
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if c.handle(ctx, requestShutdown, fields) {
				return
			}
		}
	}
}

func (c *JobCLI) handle(ctx context.Context, requestShutdown app.RequestShutdownFunc, fields []string) bool {
	switch fields[0] {
	case "start":
		duration := 10 * time.Second
		if len(fields) > 1 {
			parsed, err := time.ParseDuration(fields[1])
			if err != nil {
				fmt.Fprintf(c.out, "invalid duration: %v\n", err)
				return false
			}
			duration = parsed
		}
		job := c.jobs.StartJob(ctx, duration)
		fmt.Fprintf(c.out, "started job %d\n", job.ID)
	case "list":
		for _, job := range c.jobs.Jobs() {
			fmt.Fprintf(c.out, "%d %s %s\n", job.ID, job.Status, job.Duration)
		}
	case "cancel":
		if len(fields) < 2 {
			fmt.Fprintln(c.out, "usage: cancel <id>")
			return false
		}
		id, err := strconv.Atoi(fields[1])
		if err != nil {
			fmt.Fprintf(c.out, "invalid job id: %v\n", err)
			return false
		}
		fmt.Fprintf(c.out, "cancel %d: %v\n", id, c.jobs.CancelJob(id))
	case "quit", "exit":
		requestShutdown()
		return true
	default:
		fmt.Fprintln(c.out, "commands: start 5s | list | cancel <id> | quit")
	}
	return false
}

func scanLines(in io.Reader) (<-chan string, <-chan error) {
	lines := make(chan string)
	errs := make(chan error, 1)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(in)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		errs <- scanner.Err()
		close(errs)
	}()
	return lines, errs
}

func main() {
	verbosity := countVerbosity(os.Args[1:])
	logger := logging.Setup(logging.Config{Verbosity: verbosity, AddSource: true})

	jobs := NewJobManager()
	router := echoserver.NewRouter()
	httpAdapter := NewJobHTTP()
	cliAdapter := NewJobCLI(os.Stdin, os.Stdout)
	server := echoserver.New(echoserver.Config{Addr: ":8082"})

	a := app.New(
		context.Background(),
		app.WithLogger(logger),
		app.WithSequentialStartup(
			app.Registered(jobs),
			app.Registered(router),
			app.Managed(httpAdapter),
			app.Managed(cliAdapter),
			app.Managed(server),
		),
		app.WithStopTimeout(10*time.Second),
	)

	if err := a.Run(); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

func countVerbosity(args []string) int {
	verbosity := 0
	for _, arg := range args {
		if len(arg) > 1 && arg[0] == '-' {
			for _, r := range arg[1:] {
				if r == 'v' {
					verbosity++
				}
			}
		}
	}
	return verbosity
}
