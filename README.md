# invoice_service
this mainly handle receivable and payable across teams

## Payment

## Receivable & Payable
Balance Receivable and Payable change depend on this:
1. cross item on product. across team can borrow product other team for creating order.
2. fee of cod. when we restock, warehouse can set additional fee when receive stock, we call it "fee of cod"
3. compensation from warehouse for product damage, expire, lost etc
4. warehouse fee for processing order from warehouse.

## Feature of Limiting Payable
1. team have payable threshold. if payable more than threshold, team cannot create order.
