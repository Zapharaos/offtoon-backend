---
description: >-
  This agent is an expert of this app called Offtoon.
  Its an app that handles user search and request for LEGO bricks and sets. We make external requests to API
  clients to get data. We use runtime and websockets with our frontend to handle heavy fetching.
  Every item has core data and locale data. Every request we receive is locale dependent.
  The same request but for different locales is likely to have different results.
tools: ['insert_edit_into_file', 'replace_string_in_file', 'create_file', 'run_in_terminal', 'get_terminal_output', 'get_errors', 'show_content', 'open_file', 'list_dir', 'read_file', 'file_search', 'grep_search', 'validate_cves', 'run_subagent', 'semantic_search']
---
The app is written in GoLand.
The frontend is written with Angular as framework.

You can find the main.go file in the root of the project.

All other code can be found in the internal folder.

API communication related to the frontend:
- routing is in internal/router folder.
- http handlers are in the internal/handlers folder.

We do not own the data, we use external platforms that own this data. Therefore, we ought to be very respectful towards
those platforms. These are the API clients we use :
- TBD

They all must follow the same interface.

We should be able to configure multiple URL's for a single API client through the config/config.yaml. 
The system should be able to switch between them in case of errors or rate limits.

Every time we request data from those API clients, we must check for errors and differentiate between 2 types:
- standard : there was a problem with the request (network, timeout, etc.)
- not found : body is empty, list is empty, etc.

To improve the fetching time, we have instilled a worker pool system that is dynamically adapting to the request and
also depending on the load of our system.
You can find this at pkg/workerpool folder.

To avoid being hit by rate limits, we have a queue system in place with delays, backoff, retry and that can be adaptive.
You can find this at pkg/throttle folder.

When fetching, we shall use a toon runtime with a websocket to send progressive updates while fetching.
It handles multiple runtime, each is related to a specific webtoon with the requested arguments.
Multiple requests can be fetched at the same time, each has its own runtime.
This is in the internal/toonruntime folder, which is based on the shared pkg/wsruntime package.

- to interact from the http handlers : internal/toonruntime/handler.go
- the main logic of the runtime : internal/toonruntime/runtime.go

The main thread is a big for select loop that listens to multiple channels, like data changes, new connections, disconnections, packets from the frontend, etc.
You can find it in the internal/toonruntime/runtime.go file, in the `run` function.

The toonruntime communicates with the frontend with websockets, so each change in the runtime is directly sent to the frontend.

It works using data and packets that are available at :
- internal/toonruntime/data.go
- internal/toonruntime/packets.go
- internal/toonruntime/progress.go, while fetching the content and in order to have smooth updates

When there is an update, the runtime is updated with the new data and the runtime is
listing to a channel for data changes, the dataChange struct is defined hereby:

```go
type dataChange struct {
    Id       uuid.UUID
    Type     DataType
    Reason   DataChangeReason
    Progress Progress // Only used when working with batches
}
```

And the code to push an event is, for example here the update of a set:
```go
    h.PushChange(runtimeID, setID, DataType, DataTypeUpdated)
```
(h is the handler that contains all runtimes)

## Agent expectations

- Prefer existing patterns over inventing new ones
- When unsure, ask before changing runtime logic
- Always ensure that changes do not disrupt real-time runtime operations
- When a change is made on models that are used in the runtime, ensure that the runtime is updated accordingly
- Make sure the tests are running, and add or edit tests when you add new features or change existing ones
- When changing anything related to swagger (models, endpoints, etc.) always update the swagger documentation accordingly (swag init -ot yaml)
- Please dont check if swagger.json or swagger.yaml we're changed in the right way, i will do that myself
- At the end of all your changes, i want you to write me a prompt that i can use to edit the frontend to match the backend changes you made (angular front)