package services

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrKnowledgeIngestionWorkerUnavailable = errors.New(
		"knowledge ingestion worker dependency is unavailable",
	)
	ErrKnowledgeParserUnavailable = errors.New(
		"knowledge document parser is unavailable",
	)
)

var knowledgeWorkerKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

// KnowledgeVirusScanResult contains only the terminal trust decision. Scanner
// diagnostics are deliberately bounded before persistence and must not include
// document content, local paths, credentials, or remote URLs.
type KnowledgeVirusScanResult struct {
	Status models.VirusScanStatus
	Detail string
}

// KnowledgeVirusScanner executes outside the parser trust boundary. The
// immutable object reference includes a provider version and content digest so
// scanner and parser adapters can prove they operated on the same object.
type KnowledgeVirusScanner interface {
	Scan(
		context.Context,
		models.KnowledgeObjectReference,
	) (KnowledgeVirusScanResult, error)
}

type KnowledgeVirusScannerFunc func(
	context.Context,
	models.KnowledgeObjectReference,
) (KnowledgeVirusScanResult, error)

func (function KnowledgeVirusScannerFunc) Scan(
	ctx context.Context,
	reference models.KnowledgeObjectReference,
) (KnowledgeVirusScanResult, error) {
	return function(ctx, reference)
}

// KnowledgeDocumentParser is selected only by the trusted parser key stored
// on a queued task. Implementations run in an isolated worker and return
// untrusted text chunks that are validated again by KnowledgeService.
type KnowledgeDocumentParser interface {
	Parse(
		context.Context,
		models.KnowledgeObjectReference,
	) ([]KnowledgeChunkInput, error)
}

type KnowledgeDocumentParserFunc func(
	context.Context,
	models.KnowledgeObjectReference,
) ([]KnowledgeChunkInput, error)

func (function KnowledgeDocumentParserFunc) Parse(
	ctx context.Context,
	reference models.KnowledgeObjectReference,
) ([]KnowledgeChunkInput, error) {
	return function(ctx, reference)
}

type KnowledgeIngestionWorkerOptions struct {
	DB       *gorm.DB
	Service  *KnowledgeService
	Scanner  KnowledgeVirusScanner
	Parsers  map[string]KnowledgeDocumentParser
	WorkerID string
}

type KnowledgeIngestionWorker struct {
	db       *gorm.DB
	service  *KnowledgeService
	scanner  KnowledgeVirusScanner
	parsers  map[string]KnowledgeDocumentParser
	workerID string
}

func NewKnowledgeIngestionWorker(
	options KnowledgeIngestionWorkerOptions,
) (*KnowledgeIngestionWorker, error) {
	if options.DB == nil || options.Service == nil || options.Scanner == nil {
		return nil, ErrKnowledgeIngestionWorkerUnavailable
	}
	workerID := strings.TrimSpace(options.WorkerID)
	if workerID == "" || len(workerID) > 96 {
		return nil, errors.New("knowledge ingestion worker id is invalid")
	}
	parsers := make(map[string]KnowledgeDocumentParser, len(options.Parsers))
	for key, parser := range options.Parsers {
		normalizedKey := strings.TrimSpace(key)
		if !knowledgeWorkerKeyPattern.MatchString(normalizedKey) ||
			normalizedKey != key ||
			parser == nil {
			return nil, errors.New(
				"knowledge ingestion parser registration is invalid",
			)
		}
		parsers[normalizedKey] = parser
	}
	return &KnowledgeIngestionWorker{
		db:       options.DB,
		service:  options.Service,
		scanner:  options.Scanner,
		parsers:  parsers,
		workerID: workerID,
	}, nil
}

type KnowledgeIngestionWorkerResult struct {
	Selected    int `json:"selected"`
	Completed   int `json:"completed"`
	Quarantined int `json:"quarantined"`
	Failed      int `json:"failed"`
	Skipped     int `json:"skipped"`
}

type knowledgeIngestionWorkerOutcome uint8

const (
	knowledgeIngestionWorkerCompleted knowledgeIngestionWorkerOutcome = iota + 1
	knowledgeIngestionWorkerQuarantined
	knowledgeIngestionWorkerFailed
	knowledgeIngestionWorkerSkipped
)

// ProcessActiveProjects enumerates the server-owned active project set and
// gives every project its own bounded batch. Project is the only runtime
// boundary: a worker invocation never opens one transaction spanning projects.
//
// A trusted worker OperationContext may be supplied to restrict an explicit
// retry to that one project. Unscoped scheduler invocations enumerate the
// active projects from the control table through systemWorkerProjects.
func (worker *KnowledgeIngestionWorker) ProcessActiveProjects(
	ctx context.Context,
	perProjectLimit int,
) (KnowledgeIngestionWorkerResult, error) {
	result := KnowledgeIngestionWorkerResult{}
	if err := worker.validateProcessInput(ctx, perProjectLimit); err != nil {
		return result, err
	}
	actor := models.SystemActor(worker.workerID)
	projects, err := systemWorkerProjects(ctx, worker.db, actor)
	if err != nil {
		return result, err
	}

	var projectErrors []error
	for _, project := range projects {
		active, activeErr := worker.projectIsActive(ctx, project.Scope)
		if activeErr != nil {
			projectErrors = append(projectErrors, fmt.Errorf(
				"validate knowledge ingestion project %d: %w",
				project.Scope.ProjectID,
				activeErr,
			))
			continue
		}
		if !active {
			continue
		}
		projectResult, projectErr := worker.ProcessProject(
			ctx,
			project.Scope,
			perProjectLimit,
		)
		result.add(projectResult)
		if projectErr != nil {
			projectErrors = append(projectErrors, fmt.Errorf(
				"process knowledge ingestion project %d: %w",
				project.Scope.ProjectID,
				projectErr,
			))
		}
	}
	return result, errors.Join(projectErrors...)
}

// ProcessProject processes a bounded, explicitly scoped batch. It never scans
// all projects and therefore remains compatible with FORCE RLS and per-project
// operational budgets.
func (worker *KnowledgeIngestionWorker) ProcessProject(
	ctx context.Context,
	scope models.ProjectScope,
	limit int,
) (KnowledgeIngestionWorkerResult, error) {
	result := KnowledgeIngestionWorkerResult{}
	if err := worker.validateProcessInput(ctx, limit); err != nil {
		return result, err
	}
	if err := scope.Validate(); err != nil {
		return result, fmt.Errorf("knowledge ingestion project scope: %w", err)
	}
	workerContext, err := EnsureSystemProjectOperationContext(
		ctx,
		scope,
		models.SystemActor(worker.workerID),
		"",
		"",
	)
	if err != nil {
		return result, err
	}
	var tasks []models.KnowledgeIngestionTask
	if err := worker.withProjectDatabase(
		workerContext,
		scope,
		func(projectContext context.Context) error {
			return knowledgeScopedQuery(
				worker.db.WithContext(projectContext),
				scope,
			).Where(
				"status = ?",
				models.KnowledgeIngestionQueued,
			).Order(
				"created_at ASC, id ASC",
			).Limit(limit).Find(&tasks).Error
		},
	); err != nil {
		return result, fmt.Errorf("select queued knowledge ingestion: %w", err)
	}
	result.Selected = len(tasks)
	for _, task := range tasks {
		outcome := worker.processTask(workerContext, scope, task)
		switch outcome {
		case knowledgeIngestionWorkerCompleted:
			result.Completed++
		case knowledgeIngestionWorkerQuarantined:
			result.Quarantined++
		case knowledgeIngestionWorkerFailed:
			result.Failed++
		default:
			result.Skipped++
		}
	}
	return result, nil
}

func (worker *KnowledgeIngestionWorker) processTask(
	ctx context.Context,
	scope models.ProjectScope,
	task models.KnowledgeIngestionTask,
) knowledgeIngestionWorkerOutcome {
	var version models.KnowledgeArticleVersion
	if err := worker.withProjectDatabase(
		ctx,
		scope,
		func(projectContext context.Context) error {
			return knowledgeScopedQuery(
				worker.db.WithContext(projectContext),
				scope,
			).Where(
				"id = ? AND article_id = ? AND status = ?",
				task.VersionID,
				task.ArticleID,
				models.KnowledgeVersionDraft,
			).First(&version).Error
		},
	); err != nil {
		worker.failTask(
			ctx,
			scope,
			task.ID,
			"version_unavailable",
			"知识文档版本不可用",
		)
		return knowledgeIngestionWorkerFailed
	}

	if version.VirusScan == models.VirusScanPending {
		scan, scanErr := worker.scanner.Scan(ctx, version.ObjectReference())
		if scanErr != nil {
			scan = KnowledgeVirusScanResult{
				Status: models.VirusScanError,
				Detail: "病毒扫描服务未能完成检查",
			}
		}
		if scan.Status != models.VirusScanClean &&
			scan.Status != models.VirusScanInfected &&
			scan.Status != models.VirusScanError {
			scan.Status = models.VirusScanError
			scan.Detail = "病毒扫描返回了无效结果"
		}
		if err := worker.finalizeScanAndClaim(
			ctx,
			scope,
			task,
			version,
			scan,
		); err != nil {
			return knowledgeIngestionWorkerSkipped
		}
		if scan.Status != models.VirusScanClean {
			return knowledgeIngestionWorkerQuarantined
		}
	} else if version.VirusScan != models.VirusScanClean {
		return knowledgeIngestionWorkerSkipped
	} else if err := worker.claimParsing(ctx, scope, task.ID); err != nil {
		return knowledgeIngestionWorkerSkipped
	}
	parser, exists := worker.parsers[task.ParserKey]
	if !exists || parser == nil {
		worker.failTask(
			ctx,
			scope,
			task.ID,
			"parser_unavailable",
			"未配置受信文档解析器",
		)
		return knowledgeIngestionWorkerFailed
	}
	chunks, err := parser.Parse(ctx, version.ObjectReference())
	if err != nil {
		worker.failTask(
			ctx,
			scope,
			task.ID,
			"parser_failed",
			"文档解析失败",
		)
		return knowledgeIngestionWorkerFailed
	}
	if err := worker.storeParsedChunks(ctx, scope, task.ID, chunks); err != nil {
		worker.failTask(
			ctx,
			scope,
			task.ID,
			"chunk_validation_failed",
			"解析结果未通过安全校验",
		)
		return knowledgeIngestionWorkerFailed
	}
	if err := worker.completeIngestion(ctx, scope, task.ID); err != nil {
		worker.failTask(
			ctx,
			scope,
			task.ID,
			"completion_failed",
			"知识摄取未能完成",
		)
		return knowledgeIngestionWorkerFailed
	}
	return knowledgeIngestionWorkerCompleted
}

func (worker *KnowledgeIngestionWorker) validateProcessInput(
	ctx context.Context,
	limit int,
) error {
	if worker == nil || worker.db == nil || worker.service == nil ||
		worker.scanner == nil {
		return ErrKnowledgeIngestionWorkerUnavailable
	}
	if ctx == nil {
		return errors.New("knowledge ingestion context is required")
	}
	if scopeddb.HasTransaction(ctx) {
		return errors.New(
			"knowledge ingestion processing must start outside a database transaction",
		)
	}
	if limit < 1 || limit > 100 {
		return errors.New(
			"knowledge ingestion batch limit must be between 1 and 100",
		)
	}
	return nil
}

// projectIsActive performs a control-plane lookup only. Project is not an RLS
// protected business table, and the explicit organization/project predicate
// keeps a trusted single-project retry from processing an archived project.
func (worker *KnowledgeIngestionWorker) projectIsActive(
	ctx context.Context,
	scope models.ProjectScope,
) (bool, error) {
	var count int64
	if err := worker.db.WithContext(ctx).
		Model(&models.Project{}).
		Where(
			"organization_id = ? AND id = ? AND status = ?",
			scope.OrganizationID,
			scope.ProjectID,
			models.ProjectStatusActive,
		).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count == 1, nil
}

func (result *KnowledgeIngestionWorkerResult) add(
	other KnowledgeIngestionWorkerResult,
) {
	result.Selected += other.Selected
	result.Completed += other.Completed
	result.Quarantined += other.Quarantined
	result.Failed += other.Failed
	result.Skipped += other.Skipped
}

// withProjectDatabase is the sole database boundary for the worker. The
// callback receives a context bound to a transaction-local PostgreSQL RLS
// scope and must contain database work only.
func (worker *KnowledgeIngestionWorker) withProjectDatabase(
	ctx context.Context,
	scope models.ProjectScope,
	fn func(context.Context) error,
) error {
	return runSystemProjectOperation(
		ctx,
		worker.db,
		scope,
		models.SystemActor(worker.workerID),
		"",
		"",
		fn,
	)
}

// finalizeScanAndClaim persists the terminal scanner decision and, for a clean
// document, atomically claims the queued task for parsing. Scanner/object-store
// I/O has already completed before this short scoped transaction begins.
func (worker *KnowledgeIngestionWorker) finalizeScanAndClaim(
	ctx context.Context,
	scope models.ProjectScope,
	task models.KnowledgeIngestionTask,
	version models.KnowledgeArticleVersion,
	scan KnowledgeVirusScanResult,
) error {
	return worker.withProjectDatabase(
		ctx,
		scope,
		func(projectContext context.Context) error {
			if _, err := worker.service.MarkVersionVirusScan(
				projectContext,
				version.ID,
				scan.Status,
				boundedKnowledgeWorkerDetail(scan.Detail),
			); err != nil {
				return err
			}
			if scan.Status != models.VirusScanClean {
				return nil
			}
			_, err := worker.service.StartParsing(
				projectContext,
				task.ID,
			)
			return err
		},
	)
}

func (worker *KnowledgeIngestionWorker) claimParsing(
	ctx context.Context,
	scope models.ProjectScope,
	taskID string,
) error {
	return worker.withProjectDatabase(
		ctx,
		scope,
		func(projectContext context.Context) error {
			_, err := worker.service.StartParsing(projectContext, taskID)
			return err
		},
	)
}

// storeParsedChunks persists the bounded parser output and advances the task
// to indexing in one short transaction. Embedding or search-index I/O belongs
// after this method and before completeIngestion, never inside either database
// transaction.
func (worker *KnowledgeIngestionWorker) storeParsedChunks(
	ctx context.Context,
	scope models.ProjectScope,
	taskID string,
	chunks []KnowledgeChunkInput,
) error {
	return worker.withProjectDatabase(
		ctx,
		scope,
		func(projectContext context.Context) error {
			if _, err := worker.service.StoreChunks(
				projectContext,
				taskID,
				chunks,
			); err != nil {
				return err
			}
			return nil
		},
	)
}

func (worker *KnowledgeIngestionWorker) completeIngestion(
	ctx context.Context,
	scope models.ProjectScope,
	taskID string,
) error {
	return worker.withProjectDatabase(
		ctx,
		scope,
		func(projectContext context.Context) error {
			_, err := worker.service.CompleteIngestion(
				projectContext,
				taskID,
			)
			return err
		},
	)
}

func (worker *KnowledgeIngestionWorker) failTask(
	ctx context.Context,
	scope models.ProjectScope,
	taskID string,
	failureCode string,
	failureDetail string,
) {
	_ = worker.withProjectDatabase(
		ctx,
		scope,
		func(projectContext context.Context) error {
			return worker.service.FailIngestion(
				projectContext,
				taskID,
				failureCode,
				failureDetail,
			)
		},
	)
}

// FailIngestion is a worker-only terminal transition. It persists stable,
// bounded diagnostics instead of raw parser/scanner errors.
func (service *KnowledgeService) FailIngestion(
	ctx context.Context,
	taskID string,
	failureCode string,
	failureDetail string,
) error {
	operation, err := knowledgeWorkerOperation(ctx)
	if err != nil {
		return err
	}
	taskID = strings.TrimSpace(taskID)
	failureCode = strings.TrimSpace(failureCode)
	failureDetail = boundedKnowledgeWorkerDetail(failureDetail)
	if taskID == "" ||
		!knowledgeWorkerKeyPattern.MatchString(failureCode) ||
		failureDetail == "" {
		return errors.New("knowledge ingestion failure is invalid")
	}
	return transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		var task models.KnowledgeIngestionTask
		if err := knowledgeScopedQuery(tx, operation.Scope).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", taskID).
			First(&task).Error; err != nil {
			return knowledgeLookupError(err)
		}
		switch task.Status {
		case models.KnowledgeIngestionQueued,
			models.KnowledgeIngestionParsing,
			models.KnowledgeIngestionIndexing:
		default:
			return ErrKnowledgeIngestionState
		}
		now := service.now().UTC()
		update := knowledgeScopedQuery(
			tx.Model(&models.KnowledgeIngestionTask{}),
			operation.Scope,
		).Where(
			"id = ? AND status IN ?",
			task.ID,
			[]models.KnowledgeIngestionStatus{
				models.KnowledgeIngestionQueued,
				models.KnowledgeIngestionParsing,
				models.KnowledgeIngestionIndexing,
			},
		).UpdateColumns(map[string]any{
			"status":         models.KnowledgeIngestionFailed,
			"failure_code":   failureCode,
			"failure_detail": failureDetail,
			"completed_at":   now,
			"updated_at":     now,
		})
		if update.Error != nil {
			return fmt.Errorf("fail knowledge ingestion: %w", update.Error)
		}
		if update.RowsAffected != 1 {
			return ErrKnowledgeIngestionState
		}
		return nil
	})
}

func boundedKnowledgeWorkerDetail(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 1000 {
		value = value[:1000]
	}
	return value
}
