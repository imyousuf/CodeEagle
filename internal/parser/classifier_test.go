package parser

import (
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

func TestClassifier_JavaEntity_ToDBModel(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "UserEntity",
		Language: "java",
		FilePath: "/src/main/java/com/example/model/UserEntity.java",
		Package:  "com.example.model",
		Properties: map[string]string{
			"annotations": "Entity,Table",
		},
	}
	c.ClassifyNode(node)

	if node.Type != graph.NodeDBModel {
		t.Errorf("expected NodeDBModel, got %s", node.Type)
	}
	if node.Properties[graph.PropLayerTag] != "data_access" {
		t.Errorf("expected layer=data_access, got %s", node.Properties[graph.PropLayerTag])
	}
}

func TestClassifier_JavaDocument_ToDBModel(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "AuditLog",
		Language: "java",
		FilePath: "/src/main/java/com/example/model/AuditLog.java",
		Package:  "com.example.model",
		Properties: map[string]string{
			"annotations": "Document",
		},
	}
	c.ClassifyNode(node)

	if node.Type != graph.NodeDBModel {
		t.Errorf("expected NodeDBModel, got %s", node.Type)
	}
}

func TestClassifier_PythonModel_ToDBModel(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "User",
		Language: "python",
		FilePath: "/app/models/user.py",
		Package:  "models",
		Properties: map[string]string{
			"bases": "Model",
		},
	}
	c.ClassifyNode(node)

	if node.Type != graph.NodeDBModel {
		t.Errorf("expected NodeDBModel, got %s", node.Type)
	}
}

func TestClassifier_PythonSQLAlchemyBase_ToDBModel(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "Account",
		Language: "python",
		FilePath: "/app/models/account.py",
		Package:  "models",
		Properties: map[string]string{
			"bases": "Base",
		},
	}
	c.ClassifyNode(node)

	if node.Type != graph.NodeDBModel {
		t.Errorf("expected NodeDBModel, got %s", node.Type)
	}
}

func TestClassifier_PythonMongoDocument_ToDBModel(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "Product",
		Language: "python",
		FilePath: "/app/models/product.py",
		Package:  "models",
		Properties: map[string]string{
			"bases": "Document",
		},
	}
	c.ClassifyNode(node)

	if node.Type != graph.NodeDBModel {
		t.Errorf("expected NodeDBModel, got %s", node.Type)
	}
}

func TestClassifier_GoNewFunc_FactoryPattern(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:      graph.NodeFunction,
		Name:      "NewUserService",
		Language:  "go",
		FilePath:  "/pkg/service/user.go",
		Signature: "func NewUserService(db *sql.DB) *UserService",
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropDesignPattern] != "factory" {
		t.Errorf("expected design_pattern=factory, got %s", node.Properties[graph.PropDesignPattern])
	}
}

func TestClassifier_GoNewFunc_NoPointerReturn_NotFactory(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:       graph.NodeFunction,
		Name:       "NewConfig",
		Language:   "go",
		FilePath:   "/pkg/config/config.go",
		Signature:  "func NewConfig() Config",
		Properties: map[string]string{},
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropDesignPattern] == "factory" {
		t.Errorf("expected no factory pattern for non-pointer return, got factory")
	}
}

func TestClassifier_NameBasedDTO(t *testing.T) {
	tests := []struct {
		name     string
		nodeName string
	}{
		{"DTO suffix", "UserDTO"},
		{"Request suffix", "CreateUserRequest"},
		{"Response suffix", "ListUsersResponse"},
		{"Payload suffix", "WebhookPayload"},
		{"Command suffix", "CreateOrderCommand"},
		{"Query suffix", "GetUserQuery"},
	}

	c := NewClassifier()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &graph.Node{
				Type:     graph.NodeClass,
				Name:     tt.nodeName,
				Language: "java",
				FilePath: "/src/main/java/com/example/dto/" + tt.nodeName + ".java",
				Package:  "com.example.dto",
			}
			c.ClassifyNode(node)

			if node.Type != graph.NodeDTO {
				t.Errorf("expected NodeDTO for %q, got %s", tt.nodeName, node.Type)
			}
		})
	}
}

func TestClassifier_ViewModelDetection(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "DashboardViewModel",
		Language: "typescript",
		FilePath: "/src/viewmodels/DashboardViewModel.ts",
	}
	c.ClassifyNode(node)

	if node.Type != graph.NodeViewModel {
		t.Errorf("expected NodeViewModel, got %s", node.Type)
	}
	if node.Properties[graph.PropLayerTag] != "presentation" {
		t.Errorf("expected layer=presentation, got %s", node.Properties[graph.PropLayerTag])
	}
}

func TestClassifier_LayerClassification(t *testing.T) {
	c := NewClassifier()

	tests := []struct {
		name     string
		node     *graph.Node
		expected string
	}{
		{
			"controller -> presentation",
			&graph.Node{
				Type:     graph.NodeClass,
				Name:     "UserController",
				Language: "java",
				FilePath: "/src/main/java/com/example/controller/UserController.java",
				Package:  "com.example.controller",
				Properties: map[string]string{
					"annotations": "RestController",
				},
			},
			"presentation",
		},
		{
			"service -> business",
			&graph.Node{
				Type:     graph.NodeClass,
				Name:     "OrderService",
				Language: "java",
				FilePath: "/src/main/java/com/example/service/OrderService.java",
				Package:  "com.example.service",
				Properties: map[string]string{
					"annotations": "Service",
				},
			},
			"business",
		},
		{
			"repository -> data_access",
			&graph.Node{
				Type:     graph.NodeClass,
				Name:     "UserRepository",
				Language: "java",
				FilePath: "/src/main/java/com/example/repository/UserRepository.java",
				Package:  "com.example.repository",
				Properties: map[string]string{
					"annotations": "Repository",
				},
			},
			"data_access",
		},
		{
			"middleware -> infrastructure",
			&graph.Node{
				Type:     graph.NodeStruct,
				Name:     "AuthMiddleware",
				Language: "go",
				FilePath: "/internal/middleware/auth.go",
				Package:  "middleware",
			},
			"infrastructure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c.ClassifyNode(tt.node)
			if tt.node.Properties[graph.PropLayerTag] != tt.expected {
				t.Errorf("expected layer=%s, got %s", tt.expected, tt.node.Properties[graph.PropLayerTag])
			}
		})
	}
}

func TestClassifier_SpringAnnotations(t *testing.T) {
	c := NewClassifier()

	tests := []struct {
		name       string
		annotation string
		wantRole   string
	}{
		{"@Controller", "Controller", "controller"},
		{"@RestController", "RestController", "controller"},
		{"@Service", "Service", "service"},
		{"@Component", "Component", "service"},
		{"@Repository", "Repository", "repository"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &graph.Node{
				Type:     graph.NodeClass,
				Name:     "SomeClass",
				Language: "java",
				FilePath: "/src/main/java/com/example/SomeClass.java",
				Package:  "com.example",
				Properties: map[string]string{
					"annotations": tt.annotation,
				},
			}
			c.ClassifyNode(node)

			if node.Properties[graph.PropArchRole] != tt.wantRole {
				t.Errorf("expected role=%s for annotation %s, got %s", tt.wantRole, tt.annotation, node.Properties[graph.PropArchRole])
			}
		})
	}
}

func TestClassifier_CombinedEntityInModelPackage(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "Order",
		Language: "java",
		FilePath: "/src/main/java/com/example/model/Order.java",
		Package:  "com.example.model",
		Properties: map[string]string{
			"annotations": "Entity,Table",
		},
	}
	c.ClassifyNode(node)

	if node.Type != graph.NodeDBModel {
		t.Errorf("expected NodeDBModel, got %s", node.Type)
	}
	if node.Properties[graph.PropLayerTag] != "data_access" {
		t.Errorf("expected layer=data_access, got %s", node.Properties[graph.PropLayerTag])
	}
}

func TestClassifier_GoHandler_Controller(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeStruct,
		Name:     "UserHandler",
		Language: "go",
		FilePath: "/internal/handler/user.go",
		Package:  "handler",
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropArchRole] != "controller" {
		t.Errorf("expected role=controller, got %s", node.Properties[graph.PropArchRole])
	}
	if node.Properties[graph.PropLayerTag] != "presentation" {
		t.Errorf("expected layer=presentation, got %s", node.Properties[graph.PropLayerTag])
	}
}

func TestClassifier_GoService(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeStruct,
		Name:     "PaymentService",
		Language: "go",
		FilePath: "/internal/service/payment.go",
		Package:  "service",
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropArchRole] != "service" {
		t.Errorf("expected role=service, got %s", node.Properties[graph.PropArchRole])
	}
	if node.Properties[graph.PropLayerTag] != "business" {
		t.Errorf("expected layer=business, got %s", node.Properties[graph.PropLayerTag])
	}
}

func TestClassifier_GoRepository(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeStruct,
		Name:     "UserRepo",
		Language: "go",
		FilePath: "/internal/store/user.go",
		Package:  "store",
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropArchRole] != "repository" {
		t.Errorf("expected role=repository, got %s", node.Properties[graph.PropArchRole])
	}
	if node.Properties[graph.PropDesignPattern] != "repository" {
		t.Errorf("expected design_pattern=repository, got %s", node.Properties[graph.PropDesignPattern])
	}
}

func TestClassifier_GoStore(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeStruct,
		Name:     "EventStore",
		Language: "go",
		FilePath: "/internal/store/event.go",
		Package:  "store",
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropArchRole] != "repository" {
		t.Errorf("expected role=repository, got %s", node.Properties[graph.PropArchRole])
	}
}

func TestClassifier_PythonAPIView_Controller(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "UserView",
		Language: "python",
		FilePath: "/app/views/user.py",
		Package:  "views",
		Properties: map[string]string{
			"bases": "APIView",
		},
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropArchRole] != "controller" {
		t.Errorf("expected role=controller, got %s", node.Properties[graph.PropArchRole])
	}
}

func TestClassifier_TypeScriptDecorator_Controller(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "UserController",
		Language: "typescript",
		FilePath: "/src/controllers/UserController.ts",
		Properties: map[string]string{
			"decorators": "Controller",
		},
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropArchRole] != "controller" {
		t.Errorf("expected role=controller, got %s", node.Properties[graph.PropArchRole])
	}
}

func TestClassifier_TypeScriptInjectable_Service(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "AuthService",
		Language: "typescript",
		FilePath: "/src/services/AuthService.ts",
		Properties: map[string]string{
			"decorators": "Injectable",
		},
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropArchRole] != "service" {
		t.Errorf("expected role=service, got %s", node.Properties[graph.PropArchRole])
	}
}

func TestClassifier_TypeScriptEntityDecorator_DBModel(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "User",
		Language: "typescript",
		FilePath: "/src/entities/User.ts",
		Properties: map[string]string{
			"decorators": "Entity",
		},
	}
	c.ClassifyNode(node)

	if node.Type != graph.NodeDBModel {
		t.Errorf("expected NodeDBModel, got %s", node.Type)
	}
}

func TestClassifier_GoMiddleware_ByName(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeStruct,
		Name:     "CORSMiddleware",
		Language: "go",
		FilePath: "/internal/middleware/cors.go",
		Package:  "middleware",
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropArchRole] != "middleware" {
		t.Errorf("expected role=middleware, got %s", node.Properties[graph.PropArchRole])
	}
	if node.Properties[graph.PropLayerTag] != "infrastructure" {
		t.Errorf("expected layer=infrastructure, got %s", node.Properties[graph.PropLayerTag])
	}
}

func TestClassifier_GoMiddleware_BySignature(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:      graph.NodeFunction,
		Name:      "LoggingHandler",
		Language:  "go",
		FilePath:  "/internal/middleware/logging.go",
		Package:   "middleware",
		Signature: "func LoggingHandler(next http.Handler) http.Handler",
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropArchRole] != "middleware" {
		t.Errorf("expected role=middleware, got %s", node.Properties[graph.PropArchRole])
	}
}

func TestClassifier_JavaFilter_Middleware(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "AuthFilter",
		Language: "java",
		FilePath: "/src/main/java/com/example/filter/AuthFilter.java",
		Properties: map[string]string{
			"implements": "Filter",
		},
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropArchRole] != "middleware" {
		t.Errorf("expected role=middleware, got %s", node.Properties[graph.PropArchRole])
	}
}

func TestClassifier_BuilderPattern(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "QueryBuilder",
		Language: "java",
		FilePath: "/src/main/java/com/example/QueryBuilder.java",
		Properties: map[string]string{
			"methods": "select,from,where,Build",
		},
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropDesignPattern] != "builder" {
		t.Errorf("expected design_pattern=builder, got %s", node.Properties[graph.PropDesignPattern])
	}
}

func TestClassifier_SingletonPattern(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "ConnectionPool",
		Language: "java",
		FilePath: "/src/main/java/com/example/ConnectionPool.java",
		Properties: map[string]string{
			"methods": "getInstance,getConnection,releaseConnection",
		},
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropDesignPattern] != "singleton" {
		t.Errorf("expected design_pattern=singleton, got %s", node.Properties[graph.PropDesignPattern])
	}
}

func TestClassifier_ObserverPattern(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "EventBus",
		Language: "java",
		FilePath: "/src/main/java/com/example/EventBus.java",
		Properties: map[string]string{
			"methods": "subscribe,notify,getListeners",
		},
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropDesignPattern] != "observer" {
		t.Errorf("expected design_pattern=observer, got %s", node.Properties[graph.PropDesignPattern])
	}
}

func TestClassifier_DataAccessMethodsDetection(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "DataStore",
		Language: "python",
		FilePath: "/app/data/store.py",
		Package:  "data",
		Properties: map[string]string{
			"methods": "findAll,getById,save,deleteById,listActive",
		},
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropArchRole] != "repository" {
		t.Errorf("expected role=repository, got %s", node.Properties[graph.PropArchRole])
	}
}

func TestClassifier_PythonServicePackage(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "EmailNotifier",
		Language: "python",
		FilePath: "/app/services/email.py",
		Package:  "services",
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropArchRole] != "service" {
		t.Errorf("expected role=service, got %s", node.Properties[graph.PropArchRole])
	}
}

func TestClassifier_GoModelStruct_DBModel(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeStruct,
		Name:     "UserModel",
		Language: "go",
		FilePath: "/internal/model/user.go",
		Package:  "model",
		Properties: map[string]string{
			"fields": "ID,Name,Email,CreatedAt",
		},
	}
	c.ClassifyNode(node)

	if node.Type != graph.NodeDBModel {
		t.Errorf("expected NodeDBModel, got %s", node.Type)
	}
}

func TestClassifier_DomainModel_DDDTerms(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "OrderAggregate",
		Language: "java",
		FilePath: "/src/main/java/com/example/domain/OrderAggregate.java",
		Package:  "com.example.domain",
	}
	c.ClassifyNode(node)

	if node.Type != graph.NodeDomainModel {
		t.Errorf("expected NodeDomainModel, got %s", node.Type)
	}
	if node.Properties[graph.PropArchRole] != "domain_model" {
		t.Errorf("expected role=domain_model, got %s", node.Properties[graph.PropArchRole])
	}
	if node.Properties[graph.PropLayerTag] != "business" {
		t.Errorf("expected layer=business, got %s", node.Properties[graph.PropLayerTag])
	}
}

func TestClassifier_Classify_FullResult(t *testing.T) {
	c := NewClassifier()
	result := &ParseResult{
		FilePath: "/src/main/java/com/example/service/OrderService.java",
		Language: LangJava,
		Nodes: []*graph.Node{
			{
				Type:     graph.NodeClass,
				Name:     "OrderService",
				Language: "java",
				FilePath: "/src/main/java/com/example/service/OrderService.java",
				Properties: map[string]string{
					"annotations": "Service",
				},
			},
			{
				Type:      graph.NodeMethod,
				Name:      "createOrder",
				Language:  "java",
				FilePath:  "/src/main/java/com/example/service/OrderService.java",
				Signature: "public Order createOrder(OrderDTO dto)",
			},
		},
	}

	result = c.Classify(result)

	serviceNode := result.Nodes[0]
	if serviceNode.Properties[graph.PropArchRole] != "service" {
		t.Errorf("expected role=service, got %s", serviceNode.Properties[graph.PropArchRole])
	}
	if serviceNode.Properties[graph.PropLayerTag] != "business" {
		t.Errorf("expected layer=business, got %s", serviceNode.Properties[graph.PropLayerTag])
	}
}

func TestClassifier_PythonRepository(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "UserRepository",
		Language: "python",
		FilePath: "/app/repositories/user_repo.py",
		Package:  "repositories",
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropArchRole] != "repository" {
		t.Errorf("expected role=repository, got %s", node.Properties[graph.PropArchRole])
	}
}

func TestClassifier_NilProperties_Initialized(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeFunction,
		Name:     "helper",
		Language: "go",
		FilePath: "/internal/util/helper.go",
	}
	c.ClassifyNode(node)

	if node.Properties == nil {
		t.Error("expected Properties to be initialized, got nil")
	}
}

func TestClassifier_NoClassification_PlainFunction(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeFunction,
		Name:     "calculateTotal",
		Language: "go",
		FilePath: "/internal/calc/total.go",
		Package:  "calc",
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropArchRole] != "" {
		t.Errorf("expected no role, got %s", node.Properties[graph.PropArchRole])
	}
	if node.Properties[graph.PropDesignPattern] != "" {
		t.Errorf("expected no pattern, got %s", node.Properties[graph.PropDesignPattern])
	}
}

func TestClassifier_MultiplePatterns(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "UserRepoBuilder",
		Language: "java",
		FilePath: "/src/main/java/com/example/repository/UserRepoBuilder.java",
		Properties: map[string]string{
			"annotations": "Repository",
			"methods":     "findAll,getById,save,delete,Build",
		},
	}
	c.ClassifyNode(node)

	patterns := node.Properties[graph.PropDesignPattern]
	if patterns == "" {
		t.Fatal("expected design patterns, got empty")
	}
	if !containsPattern(patterns, "builder") {
		t.Errorf("expected builder pattern in %q", patterns)
	}
	if !containsPattern(patterns, "repository") {
		t.Errorf("expected repository pattern in %q", patterns)
	}
}

func TestClassifier_GoUseCase(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeStruct,
		Name:     "CreateOrderUseCase",
		Language: "go",
		FilePath: "/internal/usecase/create_order.go",
		Package:  "usecase",
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropArchRole] != "service" {
		t.Errorf("expected role=service, got %s", node.Properties[graph.PropArchRole])
	}
}

func TestClassifier_GoInteractor(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeStruct,
		Name:     "PaymentInteractor",
		Language: "go",
		FilePath: "/internal/interactor/payment.go",
		Package:  "interactor",
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropArchRole] != "service" {
		t.Errorf("expected role=service, got %s", node.Properties[graph.PropArchRole])
	}
}

func TestClassifier_PackageBasedDTO(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "UserInfo",
		Language: "java",
		FilePath: "/src/main/java/com/example/dto/UserInfo.java",
	}
	c.ClassifyNode(node)

	if node.Type != graph.NodeDTO {
		t.Errorf("expected NodeDTO, got %s", node.Type)
	}
}

func TestClassifier_TypeScriptSchema_DBModel(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeClass,
		Name:     "Cat",
		Language: "typescript",
		FilePath: "/src/schemas/cat.ts",
		Properties: map[string]string{
			"decorators": "Schema",
		},
	}
	c.ClassifyNode(node)

	if node.Type != graph.NodeDBModel {
		t.Errorf("expected NodeDBModel, got %s", node.Type)
	}
}

func TestClassifier_CreateFactory_NonGo(t *testing.T) {
	c := NewClassifier()
	node := &graph.Node{
		Type:     graph.NodeFunction,
		Name:     "CreateConnection",
		Language: "python",
		FilePath: "/app/factory/connection.py",
	}
	c.ClassifyNode(node)

	if node.Properties[graph.PropDesignPattern] != "factory" {
		t.Errorf("expected design_pattern=factory, got %s", node.Properties[graph.PropDesignPattern])
	}
}

// containsPattern checks if a comma-separated pattern string contains a specific pattern.
func containsPattern(patterns, target string) bool {
	for _, p := range commaSplit(patterns) {
		if p == target {
			return true
		}
	}
	return false
}
