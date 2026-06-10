# Testing
ensure `invoice_service` cover all this.
if not cover, please create testing, so we can verify easily overtime.

# Http Push Log
1. Ensure when pushing:
    - create order
    - cancel order

    Its aggregate correctly in `TeamBalance` and `TeamBalanceDailyLog`

# Payment
1. ensure create payment working properly
2. ensure accept payment working properly
3. ensure create reject payment working properly
4. ensure when payment not accepted or rejected pending amount in `TeamBalance` working properly
