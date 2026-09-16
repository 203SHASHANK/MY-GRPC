# Learning gRPC Microservices in Go

A two-service system you can run in one command, built to be **read** rather than just executed.

By the end of this you should be able to answer, without looking anything up:

1. What a `.proto` file is, and why it lives outside both services.
2. What `protoc` generates, and which parts of the code you are responsible for.
3. How a Go method call on one machine becomes bytes on a socket and a Go struct on another.
4. Where the boundary between "business logic" and "infrastructure" actually falls.
5. What this demo fakes, and what you would have to build for real.

Read it top to bottom the first time. Each part assumes the one before it.

---

## Part 0 — The idea in one sentence

> **gRPC makes calling a function in another process look like calling a function in your own.**

That is the whole pitch. Everything else — Protobuf, HTTP/2, code generation, `.proto` files — exists to make that sentence true without you writing any networking code.

Here is the payoff line from [user-service/server.go:36](user-service/server.go#L36):

```go
resp, err := s.notifClient.SendNotification(ctx, &notifpb.SendNotificationRequest{
    UserId:  id,
    Message: fmt.Sprintf("Welcome %s! Your account has been created.", req.Name),
})
```

That reads like a local method call. It is not. Between the `(` and the `)`, a struct was encoded to binary, pushed over TCP to a different container, decoded back into a struct, executed, and the answer came back the same way. You wrote none of that machinery.

### Why not just REST?

| | REST | gRPC |
|---|---|---|
| Wire format | JSON text | Protobuf binary |
| Transport | HTTP/1.1 usually | HTTP/2 always |
| Contract | a doc, a wiki, hope | a `.proto` file the compiler enforces |
| Client code | you write it | generated for you, in any language |
| Typo in a field name | found at runtime, in production | found at compile time |

The last row is the one that matters most in a team. With REST, `resp["emial"]` compiles fine and returns empty. With gRPC, `resp.Emial` does not compile.

**When to still use REST:** anything a browser or a human calls directly. That is exactly why `user-service` in this demo speaks *both* — HTTP outward, gRPC inward.

---

## Part 1 — The system

```
you (curl)
    │
    │  ① HTTP + JSON  —  POST /users
    ▼
┌──────────────────────────────────┐
│  user-service                    │
│    :8080   HTTP (you talk here)  │
│    :50051  gRPC (services talk)  │
└──────────────────────────────────┘
    │
    │  ② gRPC + Protobuf  —  SendNotification
    ▼
┌──────────────────────────────────┐
│  notification-service            │
│    :9090   gRPC only             │
└──────────────────────────────────┘
```

Two services, each owning exactly one concept:

**`user-service`** — owns users. It is unusual in that it plays three roles at once:
- an **HTTP server** on `:8080`, so you can curl it
- a **gRPC server** on `:50051`, so other services could call it
- a **gRPC client**, because it calls notification-service

**`notification-service`** — owns notifications. It speaks gRPC and nothing else. There is no HTTP port, because no human or browser ever calls it. It calls nobody — it is a leaf in the call graph.

### Why is notification a separate service at all?

The honest test: *would a second service ever need this?*

Yes. Tomorrow an `order-service` needs to notify users about shipping. With the split, it dials `notification-service:9090` and is done — `user-service` is not involved and does not even know it happened. If notifications were a function inside `user-service`, `order-service` would have to route notifications *through* the user service, which makes no sense, or duplicate the logic.

That is the real rule for splitting services: **split along ownership of a concept, not along layers of code.**

---

## Part 2 — Map of the repository

Not every folder is a service. Getting this distinction right early prevents a lot of confusion.

```
grpc-demo/
│
├── proto/                    ← NOT a service. Shared contract + generated code.
├── user-service/             ← SERVICE. Owns users.
├── notification-service/     ← SERVICE. Owns notifications.
└── docker-compose.yml        ← NOT a service. Local orchestrator.
```

### `proto/` — the contract, owned by nobody

```
proto/
├── user.proto              you write
├── notification.proto      you write
├── go.mod                  you write — this folder is its own Go module
├── userpb/
│   ├── user.pb.go          generated — structs + binary serialization
│   └── user_grpc.pb.go     generated — client + server interfaces
└── notifpb/
    ├── notification.pb.go       generated
    └── notification_grpc.pb.go  generated
```

No `main.go`. No Dockerfile. It never runs and is never deployed. It is a **library**.

**Why is it not inside the services?** Because a contract belongs to both sides equally. `notification.proto` describes what notification-service *provides* and what user-service *depends on*. Putting the file inside `notification-service/` would imply that one side owns it and can change it unilaterally. It cannot — changing it breaks the caller.

Both services pull it in as a normal Go module dependency, via a local path:

```go
// user-service/go.mod
require github.com/demo/grpc-demo/proto v0.0.0

replace github.com/demo/grpc-demo/proto => ../proto
```

The `replace` line says: "when you see this import path, use the folder next door instead of downloading it." That is what makes a monorepo like this work without publishing anything. [Part 11](#part-11--from-demo-to-production) covers what replaces `replace` when the services live in separate repos.

### The two services

Both have exactly the same four-file shape, and the split is deliberate:

```
<service>/
├── main.go      wiring only     — ports, connections, startup. No business rules.
├── server.go    logic only      — what the RPCs actually do. No networking.
├── go.mod       its own module  — its own dependency graph
└── Dockerfile   its own image   — deployed independently
```

Keep that `main.go` / `server.go` line clean and you get a useful property: **`server.go` never knows it is being called over a network.** It receives a Go struct and returns a Go struct. You can unit-test it by calling the methods directly, with no server running — which is exactly what the HTTP handlers in this demo do.

### `docker-compose.yml`

Builds the images, creates a virtual network, sets environment variables, and orders startup. In production this role is played by Kubernetes or ECS. It is a development tool, not a service.

### Written vs generated — the actual count

| | Files | Which |
|---|---|---|
| **You write** | 11 | 2 × `.proto`, 2 × `main.go`, 2 × `server.go`, 3 × `go.mod`, 2 × `Dockerfile`, `docker-compose.yml` |
| **`protoc` generates** | 4 | `user.pb.go`, `user_grpc.pb.go`, `notification.pb.go`, `notification_grpc.pb.go` |

Never hand-edit a `*.pb.go` file. Your edit disappears the next time anyone runs `protoc`. If you want something different in there, change the `.proto` and regenerate.

Those 4 generated files are a minority by count and a large majority by line count — and they do all the work you would otherwise do by hand: every byte of encoding, every interface, every network detail.

---

## Part 3 — The contract: writing `.proto`

Everything starts here. Before a line of Go exists, you decide three things: **what functions exist, what goes in, what comes back.**

### `proto/user.proto`

```proto
syntax = "proto3";

option go_package = "github.com/demo/grpc-demo/proto/userpb";

service UserService {
  rpc CreateUser (CreateUserRequest) returns (CreateUserResponse);
  rpc GetUser    (GetUserRequest)    returns (GetUserResponse);
}

message CreateUserRequest {
  string name  = 1;
  string email = 2;
}

message CreateUserResponse {
  string user_id = 1;
  string message = 2;
}

message GetUserRequest {
  string user_id = 1;
}

message GetUserResponse {
  string user_id = 1;
  string name    = 2;
  string email   = 3;
}
```

Line by line:

**`syntax = "proto3"`** — which version of the proto language. proto3 is the current standard; you will only see proto2 in old codebases.

**`option go_package = "..."`** — the Go import path stamped into the generated files. Get this wrong and your services will not be able to import the generated code, or worse, will import two copies of the same types under different paths.

**`service UserService { ... }`** — a named group of callable methods. Think of it as an interface declaration that happens to work across machines.

**`rpc CreateUser (CreateUserRequest) returns (CreateUserResponse);`** — one remote procedure call. Note the shape: **exactly one message in, exactly one message out.** Always. If you need three parameters, you put three fields in the request message. This is why every RPC has a dedicated `XxxRequest` and `XxxResponse` type even when it feels like overkill — it means you can add a fourth field next year without changing the method signature or breaking a single caller.

**`message`** — a data shape. It becomes a struct in Go, a class in Java, a dict in Python.

**`= 1`, `= 2`, `= 3`** — the single most misunderstood part of Protobuf. **These are not values and not defaults. They are field IDs.**

On the wire, Protobuf writes field *number* 1, not the string `"name"`. That is the core reason Protobuf is smaller than JSON: JSON repeats every key as text in every message; Protobuf sends a one-byte tag.

The consequence you must remember:

> **The number is the identity of the field. The name is just for humans.**

- Renaming `name` → `full_name` while keeping `= 1`: **safe.** The wire format is unchanged. Old and new code interoperate.
- Keeping the name `name` but changing `= 1` → `= 4`: **breaking.** Every existing caller now reads an empty string.
- Deleting a field and later reusing its number for something else: **silently corrupt data.** Old clients will happily decode the new field into the old one. Mark removed numbers `reserved` instead.

### `proto/notification.proto`

```proto
syntax = "proto3";

option go_package = "github.com/demo/grpc-demo/proto/notifpb";

service NotificationService {
  rpc SendNotification  (SendNotificationRequest)  returns (SendNotificationResponse);
  rpc GetNotifications  (GetNotificationsRequest)  returns (GetNotificationsResponse);
}

message SendNotificationRequest {
  string user_id = 1;
  string message = 2;
}

message SendNotificationResponse {
  string notification_id = 1;
}

message GetNotificationsRequest {
  string user_id = 1;
}

message Notification {
  string notification_id = 1;
  string user_id         = 2;
  string message         = 3;
}

message GetNotificationsResponse {
  repeated Notification notifications = 1;
}
```

Two new things here:

**`repeated`** — a list. `repeated Notification notifications = 1` becomes `[]*Notification` in Go and a JSON array on the way out.

**Messages nest.** `GetNotificationsResponse` contains `Notification` values. Messages can reference other messages freely; that is how you model anything non-flat.

Also notice: `GetNotificationsResponse` wraps the list in a message rather than returning a bare list. Proto requires this, and it turns out to be a gift — when you later need `total_count` or `next_page_token`, there is already a message to put it in.

### A trap worth knowing now: proto3 has no null

In proto3, a scalar field that was never set and a scalar field explicitly set to its zero value are **indistinguishable on the wire**. An empty `string name = 1` is simply not transmitted, and the receiver sees `""`.

So this is not expressible with a plain `string`:

> "the caller did not send an email" vs "the caller sent an empty email"

If you genuinely need that distinction (typically for PATCH-style updates), use a wrapper type or `optional`. For this demo it does not come up, but it is the kind of thing that bites people in month two.

---

## Part 4 — `protoc`: turning the contract into code

`protoc` is the Protobuf compiler. It parses `.proto` files and hands the parsed result to **plugins**, which emit code. `protoc` itself knows nothing about Go.

That is why installation is two steps.

### Step 1 — install `protoc`

```bash
curl -LO https://github.com/protocolbuffers/protobuf/releases/download/v25.3/protoc-25.3-linux-x86_64.zip
unzip -o protoc-25.3-linux-x86_64.zip -d $HOME/.local
export PATH="$HOME/.local/bin:$PATH"

protoc --version   # libprotoc 25.3
```

### Step 2 — install the two Go plugins

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
export PATH="$(go env GOPATH)/bin:$PATH"
```

| Plugin | Emits | Contains |
|---|---|---|
| `protoc-gen-go` | `user.pb.go` | the message structs + serialization |
| `protoc-gen-go-grpc` | `user_grpc.pb.go` | the client and server interfaces |

That `export PATH` line is not optional and is the single most common setup failure. `protoc` finds plugins by looking for an executable named `protoc-gen-<name>` **on your PATH**. If `$(go env GOPATH)/bin` is not on it, you get:

```
protoc-gen-go: program not found or is not executable
```

which means exactly one thing: fix your PATH.

### Step 3 — generate

Run from the `grpc-demo/` directory:

```bash
mkdir -p proto/userpb proto/notifpb

protoc --proto_path=proto \
  --go_out=proto/userpb      --go_opt=paths=source_relative \
  --go-grpc_out=proto/userpb --go-grpc_opt=paths=source_relative \
  user.proto

protoc --proto_path=proto \
  --go_out=proto/notifpb      --go_opt=paths=source_relative \
  --go-grpc_out=proto/notifpb --go-grpc_opt=paths=source_relative \
  notification.proto
```

Decoding those flags:

- `--proto_path=proto` — where to look for `.proto` files. The filename at the end (`user.proto`) is resolved relative to this, which is why it is not `proto/user.proto`.
- `--go_out=DIR` — where `protoc-gen-go` writes.
- `--go-grpc_out=DIR` — where `protoc-gen-go-grpc` writes.
- `paths=source_relative` — "mirror the input layout in the output directory." Without it, protoc recreates the full `go_package` path as nested folders and you end up with `proto/userpb/github.com/demo/grpc-demo/proto/userpb/user.pb.go`. Always pass it.

Re-run these commands every single time you edit a `.proto`. Nothing regenerates automatically.

### What comes out — `user.pb.go`

Each `message` becomes a Go struct:

```go
type CreateUserRequest struct {
    Name  string `protobuf:"bytes,1,opt,name=name,proto3"  json:"name,omitempty"`
    Email string `protobuf:"bytes,2,opt,name=email,proto3" json:"email,omitempty"`
    // ...plus unexported protobuf bookkeeping fields
}
```

Three things to notice:

1. **Naming is translated for you.** `user_id` in proto becomes `UserId` in Go. snake_case is the proto convention; the generator applies each language's conventions on the way out.
2. **Struct tags come along.** The `protobuf:"..."` tag carries the field number and wire type — that is how encoding works at runtime. The `json:"user_id,omitempty"` tag is why `json.NewEncoder` in the HTTP handlers produces `{"user_id": "..."}` and not `{"UserId": "..."}`. It also means empty fields vanish from the JSON output entirely.
3. **These are not plain structs.** They also carry `ProtoReflect()`, `Reset()`, `String()`, and the actual encoding logic that turns `{Name: "Shashank"}` into `0x0a 0x08 0x53 0x68 ...`.

### What comes out — `user_grpc.pb.go`

Each `service` becomes a matched pair of interfaces:

```go
// Client side — what a CALLER uses.
type UserServiceClient interface {
    CreateUser(ctx context.Context, in *CreateUserRequest, opts ...grpc.CallOption) (*CreateUserResponse, error)
    GetUser(ctx context.Context, in *GetUserRequest, opts ...grpc.CallOption)       (*GetUserResponse, error)
}

// Server side — what an IMPLEMENTOR must satisfy.
type UserServiceServer interface {
    CreateUser(context.Context, *CreateUserRequest) (*CreateUserResponse, error)
    GetUser(context.Context, *GetUserRequest)       (*GetUserResponse, error)
}

// Embed this in your struct to satisfy the interface safely.
type UnimplementedUserServiceServer struct{}

// Plug your implementation into a running gRPC server.
func RegisterUserServiceServer(s grpc.ServiceRegistrar, srv UserServiceServer)

// Get a client that dials a remote server.
func NewUserServiceClient(cc grpc.ClientConnInterface) UserServiceClient
```

Compare the two interfaces. The client takes `opts ...grpc.CallOption` — per-call knobs like timeouts and retries. The server does not, because the server does not make the call. Otherwise they are the same methods, and that symmetry is the point: **the compiler now guarantees that both ends of the wire agree.**

Your entire job is:
- implement `UserServiceServer` in `server.go`
- call `NewUserServiceClient` in `main.go` when you need to talk to someone

### Why two files instead of one?

- `user.pb.go` = **data.** Structs and how they serialize.
- `user_grpc.pb.go` = **transport.** How those structs travel.

They are separable on purpose. You can use Protobuf messages with no gRPC at all — to write a compact cache entry, a Kafka payload, a file on disk. Splitting the files keeps that option open and keeps the gRPC dependency out of code that only needs the types.

---

## Part 5 — The simple case: `notification-service`

Start with this one. It is a pure server — it implements RPCs and calls nobody.

### `notification-service/main.go` — wiring

```go
func main() {
    lis, err := net.Listen("tcp", ":9090")
    if err != nil {
        log.Fatalf("failed to listen: %v", err)
    }

    s := grpc.NewServer()
    pb.RegisterNotificationServiceServer(s, &notifServer{})

    log.Println("notification-service gRPC listening on :9090")
    log.Fatal(s.Serve(lis))
}
```

Four lines, four distinct jobs:

1. **`net.Listen("tcp", ":9090")`** — open a TCP socket. Pure Go standard library; nothing gRPC-specific has happened yet. gRPC does not own the socket, it is handed one.
2. **`grpc.NewServer()`** — create the gRPC engine. This is the piece that speaks HTTP/2, routes an incoming request to the right method, and does serialization in both directions. Right now it knows about zero services.
3. **`RegisterNotificationServiceServer(s, &notifServer{})`** — generated by protoc. It tells the engine: "requests for `NotificationService` go to this object." Skip this line and the server starts fine and answers every request with `Unimplemented` — a genuinely confusing failure, because nothing is obviously broken.
4. **`s.Serve(lis)`** — accept connections forever. This blocks; it only returns on shutdown or fatal error.

### `notification-service/server.go` — logic

```go
type notifServer struct {
    pb.UnimplementedNotificationServiceServer   // generated; embed it
    mu            sync.RWMutex
    notifications []*pb.Notification            // in-memory store
    counter       int
}

func (s *notifServer) SendNotification(_ context.Context, req *pb.SendNotificationRequest) (*pb.SendNotificationResponse, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    s.counter++
    id := fmt.Sprintf("notif-%d", s.counter)

    s.notifications = append(s.notifications, &pb.Notification{
        NotificationId: id,
        UserId:         req.UserId,
        Message:        req.Message,
    })

    log.Printf("[notification-service] stored notif=%s user=%s msg=%q", id, req.UserId, req.Message)
    return &pb.SendNotificationResponse{NotificationId: id}, nil
}

func (s *notifServer) GetNotifications(_ context.Context, req *pb.GetNotificationsRequest) (*pb.GetNotificationsResponse, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()

    var result []*pb.Notification
    for _, n := range s.notifications {
        if n.UserId == req.UserId {
            result = append(result, n)
        }
    }

    log.Printf("[notification-service] GetNotifications user=%s found=%d", req.UserId, len(result))
    return &pb.GetNotificationsResponse{Notifications: result}, nil
}
```

Read this file again and notice what is *absent*: no TCP, no HTTP/2, no Protobuf, no JSON, no ports, no Docker. Structs in, structs out. That is the separation working.

Three details worth understanding properly:

**Why embed `UnimplementedNotificationServiceServer`?**

It provides a default "not implemented" method for every RPC in the contract. Two things follow:

- You can implement RPCs one at a time and still compile.
- When someone adds an RPC to the `.proto` next month, your service keeps building. Callers of the new method get a clean `Unimplemented` error instead of your whole service failing to compile.

That second point is the real reason. It is what lets a contract evolve without a coordinated, all-services-at-once deploy. The trade-off is that you *can* forget to implement something and only find out at runtime — which is why the generated code makes embedding a deliberate choice rather than automatic.

**Why the mutex?**

gRPC serves **each request in its own goroutine.** Two `SendNotification` calls arriving in the same millisecond run genuinely in parallel. Without the lock, both goroutines `append` to the same slice at once and you get a data race: lost writes, or a corrupted slice header, or a crash. `RWMutex` is used rather than `Mutex` because reads (`GetNotifications`) can safely run concurrently with each other — only writes need exclusivity.

This is not a gRPC quirk to work around. **Any handler in any Go server is concurrent.** gRPC just makes it unavoidable to think about.

**Why do the signatures match exactly?**

Because they must. `notifServer` only satisfies `NotificationServiceServer` if every method matches the generated signature exactly. Change a parameter type and the program does not compile. That compile error *is* the contract being enforced — the `.proto` reaching into your Go build.

---

## Part 6 — The interesting case: `user-service`

This one is a server *and* a client at the same time, which is the normal condition for a service in the middle of a system.

### `user-service/main.go` — wiring, three jobs

```go
func main() {
    // 1. Where is notification-service? Configuration, never hardcoded.
    notifAddr := os.Getenv("NOTIFICATION_SERVICE_ADDR")
    if notifAddr == "" {
        notifAddr = "localhost:9090"      // sensible default for running outside Docker
    }

    // 2. Become a CLIENT of notification-service.
    notifConn, err := grpc.NewClient(notifAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil {
        log.Fatalf("failed to connect to notification-service: %v", err)
    }
    defer notifConn.Close()
    notifClient := notifpb.NewNotificationServiceClient(notifConn)

    srv := &userServer{
        users:       make(map[string]*User),
        notifClient: notifClient,          // injected dependency
    }

    // 3a. Be a gRPC SERVER — in the background, because Serve blocks.
    go func() {
        lis, err := net.Listen("tcp", ":50051")
        if err != nil {
            log.Fatalf("failed to listen: %v", err)
        }
        s := grpc.NewServer()
        userpb.RegisterUserServiceServer(s, srv)
        log.Println("user-service gRPC listening on :50051")
        log.Fatal(s.Serve(lis))
    }()

    // 3b. Be an HTTP SERVER — in the foreground.
    http.HandleFunc("POST /users",                    /* ... */)
    http.HandleFunc("GET /users/{id}",                /* ... */)
    http.HandleFunc("GET /notifications/{user_id}",   /* ... */)

    log.Println("user-service HTTP listening on :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

**Why is the gRPC server in a goroutine?** Because `s.Serve(lis)` and `http.ListenAndServe(...)` both block forever, and you need both listening. One goes in a goroutine, the other holds the main thread. Either one calling `log.Fatal` kills the whole process, which is the behaviour you want — a half-dead service is worse than a dead one, because load balancers keep sending it traffic.

**`grpc.NewClient` does not connect.** It is lazy: it builds a `ClientConn` — a managed, reconnecting, load-balancing handle — and returns immediately. The TCP connection is established on the first actual RPC. This is why `user-service` starts cleanly even if `notification-service` is not up yet, and why a typo'd address produces an error at call time rather than at startup.

**`insecure.NewCredentials()` means no TLS.** The name is deliberately uncomfortable. It is correct for a local demo on a private Docker network and wrong for anything else. In production you use mutual TLS so each service proves its identity to the other.

**One connection, reused forever.** `notifConn` is created once in `main` and shared. Do not open a connection per request — you would pay the TCP and HTTP/2 handshake every time and lose all connection pooling. A `ClientConn` is safe for concurrent use by many goroutines; that is what it is designed for.

**Dependency injection, plainly.** `notifClient` is built in `main.go` and handed to `userServer`. That is why `server.go` has no idea where notification-service lives — and why a test can hand it a fake client with no network at all.

### `user-service/server.go` — logic

```go
type userServer struct {
    userpb.UnimplementedUserServiceServer
    mu          sync.RWMutex
    users       map[string]*User
    counter     int
    notifClient notifpb.NotificationServiceClient   // injected from main.go
}

func (s *userServer) CreateUser(ctx context.Context, req *userpb.CreateUserRequest) (*userpb.CreateUserResponse, error) {
    s.mu.Lock()
    s.counter++
    id := fmt.Sprintf("user-%d", s.counter)
    s.users[id] = &User{ID: id, Name: req.Name, Email: req.Email}
    s.mu.Unlock()                       // released BEFORE the network call

    log.Printf("[user-service] created user=%s name=%s", id, req.Name)

    // The cross-service call.
    resp, err := s.notifClient.SendNotification(ctx, &notifpb.SendNotificationRequest{
        UserId:  id,
        Message: fmt.Sprintf("Welcome %s! Your account has been created.", req.Name),
    })
    if err != nil {
        log.Printf("[user-service] notification failed: %v", err)   // logged, not returned
    } else {
        log.Printf("[user-service] notification sent notif_id=%s", resp.NotificationId)
    }

    return &userpb.CreateUserResponse{UserId: id, Message: "user created successfully"}, nil
}
```

Three design decisions in this small function, all worth copying:

**The mutex is unlocked before the RPC, not with `defer`.** Look closely: it is `s.mu.Unlock()` on its own line, not `defer s.mu.Unlock()` at the top. With `defer`, the lock would be held for the entire duration of a *network call*. Every other `CreateUser` in the process would queue behind a remote service you do not control. **Never hold a lock across a network call.** Lock, mutate memory, unlock, then do I/O.

**The notification failure is logged, not returned.** If notification-service is down, the user was still created — the write already succeeded. Failing the whole request would be a lie, and the caller retrying would create a duplicate user. So the error is logged and the request succeeds.

This is a real engineering decision, and it has a real cost: **the notification is now silently lost.** The honest production version writes the pending notification to a queue or an outbox table so a retry can pick it up later. What you must not do is make that choice by accident. Ask, for every cross-service call: *is this thing essential to the operation, or merely desirable?* Essential means propagate the error. Desirable means log it and compensate somewhere else.

**`ctx` is passed through.** The `ctx` from the caller flows into the outgoing RPC. That means a cancellation or deadline upstream automatically propagates downstream: if the HTTP client hangs up, the in-flight call to notification-service is cancelled too, and the work stops instead of burning CPU for a response nobody will read. Always pass the incoming `ctx` down. Never substitute `context.Background()` to "make an error go away" — you are switching off cancellation for the whole chain.

### The decoupling, stated plainly

`user-service` does not know whether notification-service writes to Postgres, pushes to Firebase, sends email via SES, or throws everything away. It knows one thing: the `.proto`.

Which means you can rewrite notification-service in Rust, move it to another cloud, or replace it wholesale with a managed service — and `user-service` does not change by one character, so long as the contract holds.

**That is what "decoupled" actually means.** Not "in separate folders" — separately *changeable*.

---

## Part 7 — Packaging: Docker

### The Dockerfile, and the wrinkle this repo has

```dockerfile
# ---- Stage 1: build ----
FROM golang:1.22-alpine AS builder
WORKDIR /app

# The shared proto module must be inside the build context,
# because go.mod says: replace ... => ../proto
COPY proto/ ./proto/

WORKDIR /app/user-service
COPY user-service/go.mod user-service/go.sum ./
RUN go mod download                 # cached layer: only redone when deps change
COPY user-service/ .                # source copied after deps
RUN go build -o user-service .

# ---- Stage 2: run ----
FROM alpine:3.21
WORKDIR /app
COPY --from=builder /app/user-service/user-service .
EXPOSE 8080 50051
CMD ["./user-service"]
```

**Why two stages?** The Go toolchain image is roughly 600 MB. The final Alpine image is roughly 10 MB. You need a compiler to build and not to run, so the compiler stays in stage 1 and only the binary crosses over. Smaller images deploy faster, cost less to store, and expose far less attack surface — there is no shell-full-of-tools sitting in your production container.

**Why copy `go.mod`/`go.sum` before the source?** Docker caches layers and invalidates everything after the first change. Dependencies change rarely; source changes constantly. Copying the manifests first means `go mod download` is a separate layer that survives ordinary code edits. Copy everything at once instead and you re-download the world on every build.

**Why does this Dockerfile copy `proto/` and use paths like `user-service/go.mod`?** Because of the `replace => ../proto` directive. The build context must contain both the service *and* the shared module, so the context is the repo root, not the service folder — which is why the compose file says:

```yaml
build:
  context: .
  dockerfile: user-service/Dockerfile
```

Every `COPY` path is therefore relative to `grpc-demo/`, not to `user-service/`. This is the price of a local shared module, and the clearest practical argument for publishing the proto module properly once you have more than a couple of services.

> **⚠️ Known mismatch in this repo.** The Dockerfiles pin `golang:1.22-alpine`, but every `go.mod` declares `go 1.25.0`. Go 1.21+ will try to auto-download the newer toolchain mid-build, which is slow and fails outright in a network-restricted builder. The fix is one line in each Dockerfile: `FROM golang:1.25-alpine AS builder`. Keep your base image at or above the `go` directive in `go.mod`.

### `docker-compose.yml`

```yaml
version: "3.9"

services:
  notification-service:
    build:
      context: .
      dockerfile: notification-service/Dockerfile
    expose:
      - "9090"                       # visible inside the network only
    healthcheck:
      test: ["CMD-SHELL", "nc -z localhost 9090 || exit 1"]
      interval: 10s
      timeout: 5s
      retries: 5

  user-service:
    build:
      context: .
      dockerfile: user-service/Dockerfile
    ports:
      - "8080:8080"                  # published to your machine
      - "50051:50051"
    environment:
      - NOTIFICATION_SERVICE_ADDR=notification-service:9090
    depends_on:
      notification-service:
        condition: service_healthy
```

**`expose` vs `ports`.** `expose: 9090` makes the port reachable from other containers on the network and *not* from your laptop. `ports: "8080:8080"` publishes to the host. notification-service is internal by design, so nothing outside Docker can reach it — the network topology enforces the architecture.

**Service names are DNS names.** Compose creates a virtual network and registers each service name in its internal DNS. Inside the network, `notification-service:9090` resolves to that container's IP. No IP addresses anywhere, and it keeps working when containers restart with new IPs.

**Configuration through the environment.** `NOTIFICATION_SERVICE_ADDR` is read by `os.Getenv` in `main.go`. The same binary runs unchanged against `localhost:9090` on your laptop, `notification-service:9090` in Compose, and a Kubernetes service DNS name in production. The image never changes; only the environment does.

**`condition: service_healthy` beats plain `depends_on`.** Plain `depends_on` waits for the container to *start*, which says nothing about whether the process inside is ready. The healthcheck actually probes port 9090 with `nc` and only then releases user-service.

Strictly speaking, gRPC's lazy connections mean user-service would survive without this — it would just fail the first call or two and reconnect. The healthcheck is here because deterministic startup makes the demo *teachable*: you always see the same log order. In production you want both — ordered startup *and* clients that tolerate a dependency restarting at 3 a.m.

---

## Part 8 — Run it

```bash
cd grpc-demo
docker compose up --build
```

You should see:

```
notification-service-1  | notification-service gRPC listening on :9090
user-service-1          | user-service gRPC listening on :50051
user-service-1          | user-service HTTP listening on :8080
```

Three listeners, exactly as designed. Leave this terminal open — the logs are the best part — and use a second one for curl.

### Create a user

```bash
curl -s -X POST http://localhost:8080/users \
  -H "Content-Type: application/json" \
  -d '{"name": "Shashank", "email": "shashank@example.com"}' | jq
```

```json
{
  "user_id": "user-1",
  "message": "user created successfully"
}
```

Now look at the other terminal. **This is the moment the whole demo exists for:**

```
user-service-1          | [user-service] created user=user-1 name=Shashank
notification-service-1  | [notification-service] stored notif=notif-1 user=user-1 msg="Welcome Shashank! Your account has been created."
user-service-1          | [user-service] notification sent notif_id=notif-1
```

One curl. Two processes. A network hop in the middle that nobody wrote networking code for.

### Create another, then read them back

```bash
curl -s -X POST http://localhost:8080/users \
  -H "Content-Type: application/json" \
  -d '{"name": "Priya", "email": "priya@example.com"}' | jq

curl -s http://localhost:8080/users/user-1 | jq
```

```json
{
  "user_id": "user-1",
  "name": "Shashank",
  "email": "shashank@example.com"
}
```

```bash
curl -s http://localhost:8080/notifications/user-1 | jq
```

```json
{
  "notifications": [
    {
      "notification_id": "notif-1",
      "user_id": "user-1",
      "message": "Welcome Shashank! Your account has been created."
    }
  ]
}
```

That last one is the proof. The notification was never stored in user-service — it lives in the other container's memory. user-service fetched it over gRPC and translated it to JSON for you.

(`jq` just pretty-prints. Drop it if you do not have it.)

### Experiments that teach more than reading

1. **Kill the dependency.** `docker compose stop notification-service`, then create a user. It still succeeds — with `notification failed: ...` in the logs. You just watched the "log it, do not fail" decision from Part 6 play out.
2. **Prove notification-service is private.** `curl localhost:9090` → connection refused. It is not published to your host, by design.
3. **Break the contract on purpose.** Change a field number in `user.proto`, regenerate, rebuild. Watch what breaks and where.
4. **Watch fields vanish.** `POST /users` with `{"name": "X", "email": ""}`, then `GET` that user. No `email` key in the response at all — that is `omitempty` on a proto3 zero value, from Part 3.

---

## Part 9 — One request, all the way down

This is the section to re-read once everything else makes sense.

First, a correction to a common misreading of this demo. **There is exactly one real gRPC network call in the whole system:** user-service → notification-service. The HTTP handlers call `srv.CreateUser(...)` and `srv.GetUser(...)` as ordinary in-process Go method calls — same struct types, no serialization, no socket. That is not a shortcut, it is the payoff of keeping `server.go` free of transport code: the same method serves both an HTTP handler and a gRPC request.

`user-service`'s own gRPC server on `:50051` is registered and listening, but nothing in this demo calls it. It is there to show the shape of a service that is reachable both ways — the entry point a future `order-service` would use.

```
you: curl -X POST localhost:8080/users -d '{"name":"Shashank", ...}'
  │
  │  HTTP/1.1, JSON text over TCP
  ▼
┌─ user-service :8080 ─────────────────────────────────────────────┐
│                                                                   │
│  HTTP handler                                                     │
│    json.Decode(body) → CreateUserRequest{Name:"Shashank", ...}    │
│                                                                   │
│  srv.CreateUser(ctx, req)      ← plain Go call, no network        │
│    lock → users["user-1"] = ... → unlock                          │
│                                                                   │
│  s.notifClient.SendNotification(ctx, &SendNotificationRequest{…}) │
│    ┌─ generated code takes over ────────────────────────────────┐ │
│    │ 1. struct → Protobuf binary                                │ │
│    │      {user_id:"user-1", message:"Welcome…"}                │ │
│    │      → 0x0a 0x06 0x75 0x73 0x65 0x72 0x2d 0x31 …           │ │
│    │      field 1, 6 bytes, "user-1" — no key names on the wire │ │
│    │ 2. wrapped in an HTTP/2 frame, path                        │ │
│    │      /NotificationService/SendNotification                 │ │
│    │ 3. sent over TCP to notification-service:9090              │ │
│    └────────────────────────────────────────────────────────────┘ │
└───────────────────────────────────────────────────────────────────┘
  │
  ▼
┌─ notification-service :9090 ─────────────────────────────────────┐
│  gRPC engine reads the frame, routes by path                     │
│    4. binary → SendNotificationRequest struct                    │
│    5. your SendNotification() runs — appends to the slice        │
│    6. returns SendNotificationResponse{NotificationId:"notif-1"} │
│    7. struct → binary → back over the same connection            │
└──────────────────────────────────────────────────────────────────┘
  │
  ▼
┌─ user-service ───────────────────────────────────────────────────┐
│    8. binary → SendNotificationResponse struct                   │
│    9. resp.NotificationId == "notif-1"  — logged                 │
│   10. return CreateUserResponse{UserId:"user-1", …}              │
│                                                                   │
│  HTTP handler: json.Encode(resp)  ← uses the generated json tags │
└───────────────────────────────────────────────────────────────────┘
  │
  ▼
you: {"user_id":"user-1","message":"user created successfully"}
```

Steps 1, 2, 3, 4, 7 and 8 — every byte of encoding, framing and decoding — came from the four `*.pb.go` files. You wrote steps 5 and 9 and the business logic around them.

**That ratio is the entire value proposition of gRPC.**

---

## Part 10 — What this demo deliberately fakes

A tutorial that does not name its own shortcuts teaches bad habits. Here is every one of them.

**State is in memory.** Both services store data in a map and a slice. Restart a container and everything is gone. Worse, this only works because there is exactly one replica of each service — run two copies of notification-service behind a load balancer and each holds a different half of the data. Real state goes in a database that all replicas share.

**IDs are counters.** `user-1`, `notif-1`, from an `int` in a struct. Two replicas would immediately hand out the same ID. Use UUIDs, or let the database generate them.

**No TLS.** `insecure.NewCredentials()` everywhere. On a private Docker network with nothing published, acceptable. Across a real network, it means any party in the path can read and modify every message.

**No deadlines.** Nothing in this code calls `context.WithTimeout`. The `ctx` from the HTTP request does propagate cancellation, but there is no upper bound on how long a call may take. If notification-service hangs instead of failing, `CreateUser` hangs with it, HTTP requests pile up, and user-service falls over because of a dependency that never said no. **Every outbound RPC in production should have an explicit deadline:**

```go
ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
defer cancel()
resp, err := s.notifClient.SendNotification(ctx, req)
```

This is the single most valuable line to add to this codebase.

**Errors are Go errors, not gRPC status codes.** `GetUser` returns `fmt.Errorf("user %s not found", ...)`, which reaches the caller as `Unknown` — indistinguishable from a crash. gRPC has a proper code set, and callers make retry decisions based on it:

```go
return nil, status.Errorf(codes.NotFound, "user %s not found", req.UserId)
```

`NotFound` tells a caller "do not retry, this will never succeed." `Unavailable` tells it "retry with backoff." `Unknown` tells it nothing, so clients guess — usually wrongly.

**No input validation.** `CreateUser` accepts an empty name and a malformed email without complaint. Remember proto3 has no null: a missing field and an empty field look identical, so validation is a thing *you* write, in the handler, always.

**No tests.** The structure is unusually test-friendly — `server.go` has no infrastructure in it, and `notifClient` is an injected interface, so a fake client needs no network — but not a single test exists.

**Lost notifications.** Covered in Part 6: if the notification call fails, the notification is gone forever. A real system uses an outbox table or a queue.

**A local `replace` directive.** `replace => ../proto` only works while everything lives in one checkout. It cannot survive the services moving to separate repositories.

---

## Part 11 — From demo to production

### The proto module problem

Today, both services find the contract through:

```go
replace github.com/demo/grpc-demo/proto => ../proto
```

That is fine for a monorepo. The moment services live in different repositories, it breaks — there is no `../proto` to point at.

**The fix: publish the contract as a versioned artifact.**

```
github.com/yourcompany/protos/        ← its own repository
├── user/v1/user.proto
├── notification/v1/notification.proto
└── order/v1/order.proto
```

```
.proto merged to main
        │
        │  CI runs protoc (or buf) automatically
        ▼
generated code for every language you use
        │
        │  CI publishes and tags it
        ▼
github.com/yourcompany/protos/gen/go @ v1.3.0
```

Each service then depends on it like any other library:

```go
// user-service/go.mod
require github.com/yourcompany/protos/gen/go v1.3.0
```

What that buys you:

- No hand-copied generated files, ever.
- Each service pins an explicit version, so nobody is upgraded without noticing.
- Two services can run different versions during a rollout — which is mandatory, because you cannot deploy everything at once.
- Generated code is never committed by a human, so it cannot drift from the `.proto`.

### Catching breaking changes before they ship

[`buf`](https://buf.build) is the modern toolchain for this. Its most valuable command is `buf breaking`, run in CI on every pull request:

```proto
// v1.2.0
message GetUserResponse {
  string user_id = 1;
  string name    = 2;
  string email   = 3;   ← someone deletes this line in v1.3.0
}
```

```
FAIL: Field "email" on message "GetUserResponse" was deleted.
Existing callers reading .Email will silently receive "".
This is a breaking change. PR blocked.
```

Read that failure message closely. The danger is not a crash — it is **silence**. Removing a field does not break the caller's build; the field simply arrives empty forever. The billing service keeps running and starts emailing nobody. `buf breaking` turns a silent production incident into a blocked pull request.

### The full picture

| Concern | This demo | Production |
|---|---|---|
| Storage | in-memory map / slice | Postgres, DynamoDB, Redis |
| IDs | incrementing counter | UUID, or database-generated |
| Transport security | `insecure.NewCredentials()` | mutual TLS between services |
| Deadlines | none | `context.WithTimeout` on every outbound call |
| Retries | none | backoff + jitter, only on `Unavailable` / `DeadlineExceeded` |
| Errors | `fmt.Errorf` → `Unknown` | `status.Errorf` with proper `codes.*` |
| Failure isolation | none | circuit breakers, bulkheads |
| Lost notifications | dropped, logged | outbox table or message queue |
| Proto distribution | local `replace` | separate repo, versioned module, `buf` |
| Breaking changes | nothing stops you | `buf breaking` in CI |
| Orchestration | docker compose | Kubernetes / ECS |
| Service discovery | Docker DNS | Kubernetes DNS, Consul, Cloud Map |
| Health checks | `nc -z` on the port | gRPC health checking protocol |
| Observability | `log.Printf` | structured logs, Prometheus, OpenTelemetry tracing |
| Testing | none | unit tests on `server.go`, integration tests on the wire |

Work down that table in order. Deadlines and status codes first — they are a few lines each and they prevent the failures that take a system down at 3 a.m.

---

## Reference

### Glossary

| Term | What it is | Where it shows up here |
|---|---|---|
| `.proto` file | The contract: RPCs and message shapes | [proto/user.proto](proto/user.proto) |
| `protoc` | Compiler that reads `.proto` and drives plugins | run by hand here; in CI in production |
| `protoc-gen-go` | Plugin emitting message structs | produces `*.pb.go` |
| `protoc-gen-go-grpc` | Plugin emitting client/server interfaces | produces `*_grpc.pb.go` |
| field number (`= 1`) | The field's real identity on the wire | every `message` |
| `repeated` | A list; becomes `[]T` in Go | `GetNotificationsResponse` |
| `*.pb.go` | Generated structs + serialization | [proto/userpb/user.pb.go](proto/userpb/user.pb.go) |
| `*_grpc.pb.go` | Generated client + server interfaces | [proto/userpb/user_grpc.pb.go](proto/userpb/user_grpc.pb.go) |
| `UnimplementedXxxServer` | Embedded default impl; lets contracts grow | both `server.go` files |
| `grpc.NewServer()` | The engine: HTTP/2, routing, serialization | both `main.go` files |
| `RegisterXxxServer` | Binds your impl into that engine | both `main.go` files |
| `grpc.NewClient()` | Lazy, reconnecting connection handle | [user-service/main.go](user-service/main.go) |
| `NewXxxClient` | Typed client over that connection | [user-service/main.go](user-service/main.go) |
| `insecure.NewCredentials()` | No TLS — dev only | [user-service/main.go](user-service/main.go) |
| `context.Context` | Carries deadlines, cancellation, metadata | every RPC signature |
| `sync.RWMutex` | Guards state against concurrent handlers | both `server.go` files |
| `replace` directive | Points a module at a local folder | both service `go.mod` files |

### Commands

```bash
# Regenerate Go code after editing any .proto (run from grpc-demo/)
protoc --proto_path=proto \
  --go_out=proto/userpb      --go_opt=paths=source_relative \
  --go-grpc_out=proto/userpb --go-grpc_opt=paths=source_relative \
  user.proto

# Run everything
docker compose up --build

# Rebuild one service only
docker compose up --build user-service

# Follow one service's logs
docker compose logs -f notification-service

# Tear down
docker compose down
```

### Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `protoc-gen-go: program not found` | Plugin not on PATH | `export PATH="$(go env GOPATH)/bin:$PATH"` |
| Generated files land in deeply nested folders | Missing `paths=source_relative` | Add the flag to both `--go_opt` and `--go-grpc_opt` |
| `unknown service NotificationService` | Forgot `RegisterXxxServer` | Add it in `main.go` before `Serve` |
| `connection refused` on the first RPC | Dependency not up, or wrong address | Check `NOTIFICATION_SERVICE_ADDR` and the healthcheck |
| Docker build downloads a Go toolchain | Base image older than `go.mod` | Bump `FROM golang:1.25-alpine` |
| `cannot find module ../proto` | Building with the wrong context | Build from the repo root: `context: .` |
| A field is always empty | Field number changed, or proto3 zero value | Diff the `.proto`; check whether it was ever set |
| JSON response missing a key | `omitempty` on a zero value | Expected behaviour, not a bug |

### Where to go next

1. **Add deadlines and `status` codes.** An hour's work; the biggest single improvement to this code.
2. **Write tests for `server.go`.** The structure already supports it — inject a fake `NotificationServiceClient`.
3. **Add a third service.** `order-service` calling notification-service directly is what proves the architecture was worth it.
4. **Try streaming.** Every RPC here is unary — one request, one response. gRPC also does server streaming, client streaming, and bidirectional. `rpc WatchNotifications(Req) returns (stream Notification)` is a good next `.proto` to write.
5. **Add an interceptor.** gRPC's version of middleware — logging, auth, metrics, tracing on every call, written once.
