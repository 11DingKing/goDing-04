# Bug Reproduction

## 包的性质

当前 test_model_fix 保存的是被测模型修复后的结果源码，不是初始含 Bug 源码。要复现原始缺陷，必须检出下面固定的 parent SHA；不要在当前修复结果源码上期待重新出现修复前失败。生成系统使用的可信验证补丁和完整验证日志仅在本地留存，不提交到结果分支。

## 问题现象

旺季运力紧张，我们发现有货主临期退舱之后，那些冷藏舱位就再也卖不出去了，航次上明明没有货却一直显示满舱，排队的货主全被挡在外面。

这个航次 40 小时后开航（已经在 72 小时窗口内），一共 2 个冷藏舱位，全部锁给了一个货主，然后他退舱：

```
$ curl -s -X POST localhost:51108/api/bookings/booking-2/cancel
"status":"cancelled"
"deposit_refunded":false
"deposit_forfeited":true
"cancellation_note":"cancelled within 72h of sailing; deposit forfeited"

$ curl -s localhost:51108/api/voyages/voyage-1
"total_reefer_slots":2
"booked_reefer_slots":2

$ curl -s -X POST localhost:51108/api/bookings -d @next.json
{"error":"voyage: insufficient reefer slots: available 0, requested 2"}
http_code=409
```

定金按 72 小时规则没收，这部分是对的。但订舱已经取消了，booked_reefer_slots 还是 2，运力根本没退回来，下一个排队的货主锁不上。72 小时之前提前退的那种是好的，舱位马上就放出来了，只有临期这种会卡住。同一个航次上多退几票，可用舱位就越来越少，最后跟实际货量完全对不上。

退舱就应该把冷藏舱位退回航次运力池，好让我们转售，跟定金怎么处理是两回事。帮我修掉，让退舱后运力立刻恢复、能被下一个货主锁走。已有测试跑一遍不要有回归。

## 含 Bug 版本

- 仓库：11DingKing/goDing-04
- 仓库地址：https://github.com/11DingKing/goDing-04.git
- parent SHA：1f46c6a503523232a9789501c4476a784d521921

## 复现步骤

```bash
git clone -- https://github.com/11DingKing/goDing-04.git bug-repro
cd bug-repro
git checkout --detach 1f46c6a503523232a9789501c4476a784d521921
go test ./internal/app/ ./internal/httpapi/ -run "TestLateCancellationReleasesCapacity|TestCapacityResellableAfterLateCancellation|TestCancellationAtDeadlineInstantRefundsAndReleases|TestEarlyCancellationReleasesCapacity|TestMixedCancellationsKeepCapacityConsistent|TestHTTPLateCancellationFreesCapacity" -count=1 -timeout=120s
```

## 双架构完整错误信息

### linux/amd64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/app/ ./internal/httpapi/ -run "TestLateCancellationReleasesCapacity|TestCapacityResellableAfterLateCancellation|TestCancellationAtDeadlineInstantRefundsAndReleases|TestEarlyCancellationReleasesCapacity|TestMixedCancellationsKeepCapacityConsistent|TestHTTPLateCancellationFreesCapacity" -count=1 -timeout=120s
--- FAIL: TestLateCancellationReleasesCapacity (0.01s)
    cancellation_test.go:66: expected 0 booked slots after cancelling the only booking, got 3
--- FAIL: TestCapacityResellableAfterLateCancellation (0.00s)
    cancellation_test.go:90: expected the freed capacity to be lockable again: voyage: insufficient reefer slots: available 0, requested 2
--- FAIL: TestMixedCancellationsKeepCapacityConsistent (0.00s)
    cancellation_test.go:160: expected 4 booked slots (only the surviving booking), got 7
FAIL
FAIL	arcticfreight/internal/app	0.084s
--- FAIL: TestHTTPLateCancellationFreesCapacity (0.02s)
    handler_test.go:252: expected 0 booked reefer slots after cancellation, got 2
FAIL
FAIL	arcticfreight/internal/httpapi	0.066s
FAIL

```

stderr：

```text
(empty)
```

### linux/arm64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/app/ ./internal/httpapi/ -run "TestLateCancellationReleasesCapacity|TestCapacityResellableAfterLateCancellation|TestCancellationAtDeadlineInstantRefundsAndReleases|TestEarlyCancellationReleasesCapacity|TestMixedCancellationsKeepCapacityConsistent|TestHTTPLateCancellationFreesCapacity" -count=1 -timeout=120s
--- FAIL: TestLateCancellationReleasesCapacity (0.00s)
    cancellation_test.go:66: expected 0 booked slots after cancelling the only booking, got 3
--- FAIL: TestCapacityResellableAfterLateCancellation (0.00s)
    cancellation_test.go:90: expected the freed capacity to be lockable again: voyage: insufficient reefer slots: available 0, requested 2
--- FAIL: TestMixedCancellationsKeepCapacityConsistent (0.00s)
    cancellation_test.go:160: expected 4 booked slots (only the surviving booking), got 7
FAIL
FAIL	arcticfreight/internal/app	0.002s
--- FAIL: TestHTTPLateCancellationFreesCapacity (0.00s)
    handler_test.go:252: expected 0 booked reefer slots after cancellation, got 2
FAIL
FAIL	arcticfreight/internal/httpapi	0.003s
FAIL

```

stderr：

```text
(empty)
```

## 通过条件

定向测试与全量回归在 linux/amd64、linux/arm64 双架构下均通过。
72 小时窗口内取消：定金仍然没收（deposit_forfeited=true、deposit_refunded=false），同时冷藏舱位全部退回，booked_reefer_slots 归零、可用舱位恢复。
退回的运力可以被下一个货主实际锁走（HTTP 返回 201，而非 409 insufficient reefer slots）。
72 小时截止时刻整取消仍判定为可退定金，并同样释放舱位；提前取消的既有行为不变。
同一航次上混合提前取消与临期取消后，booked_reefer_slots 等于剩余未取消订舱的箱量之和。
不得放宽或删除既有断言，不得改测试来迁就实现。
