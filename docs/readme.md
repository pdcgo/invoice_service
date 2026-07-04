# Invoice Service
This Is part of Submodule of [Warehouse Infra](https://github.com/pdcgo/warehouse_infra). In Warehouse Infra this is live in folder `./invoice_service`.<br>
This Service planned and Intended for replacing legacy Product System that exists in Warehouse Infra. Its planned for microservice and planned to more dependentless, separating domain purpose for better developing big and complex system that exists in Warehouse Infra.<br>
For now its just be candidate for Take over Selling System In Warehouse Infra legacy.
Status for this development is still in progress and not completely take over legacy system.

1. for database schema related, read this [Database Schema](database-schema.md).
2. this service also follow protobuf guideline of this. [Warehouse Infra Proto Guideline](../../docs/proto-guideline.md)


## Authentication & Authorization
1. Use v2 roling system. not legacy system. [Read This](../../user_service/docs/readme.md)


## Connect RPC Spec
`InvoiceService` heavyly depend `connect-rpc` to serve and creating apis and grpc. Why we use `connectrpc` because its can be two mode as pure grpc and grpc-web that interact like web. And also supported http2. This service have several rpc:

1. Balance time-series named `TeamBalanceTimeline`.
    - Returns a payable/receivable balance series for the scoped team, bucketed daily/monthly/yearly in Asia/Jakarta. Each bucket's metrics are selected per request via `data_types`: end-of-period `PAYABLE_BALANCE` / `RECEIVABLE_BALANCE`, in-period net `PAYABLE_CHANGE` / `RECEIVABLE_CHANGE`, and `TOTAL_PAYMENT` (accepted payments).
    - The change metrics additionally carry a per-`invoice_iface.v2.BalanceChangeType` breakdown (`ChangeSumAmount`: `change_type`, `amount`, `transaction_count`) — e.g. adjustment, warehouse_fee, cod_fee, product_fee, payment, stock_problem — whose amounts sum to the bucket's net change.