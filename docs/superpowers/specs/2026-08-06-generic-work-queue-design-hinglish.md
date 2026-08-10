# Generic Work Queue Design (Hinglish)

Date: 2026-08-06

> **Note:** Ye [2026-08-05-generic-work-queue-design.md](2026-08-05-generic-work-queue-design.md)
> ka Hinglish version hai. Code, SQL aur signatures bilkul wahi hain; sirf
> samjhaane wali baatein Hinglish me hain.
>
> Section headings dono me bilkul same rakhe gaye hain, taaki
> `diff <(grep '^#' en.md) <(grep '^#' hi.md)` se drift turant dikh jaye.
>
> **Canonical English waali hai.** Design badle toh pehle usko update karo, phir
> ise. Dono me farak dikhe toh English waali sahi maani jaaye.

## Problem

Jo writes Postgres aur kisi external system dono me jaani chahiye — wo abhi
teen alag tarikon se handle hoti hain.

`CreateStack` sahi hai: stack row aur `authorization_outbox` row ek hi
transaction me likhta hai, phir inline delivery try karta hai aur intent complete
mark kar deta hai.

`AssignStackRole` aur `RevokeStackRole` sahi **nahi** hain. Wo seedha OpenFGA ko
call karte hain, peechhe koi durable intent nahi. Aur assign path pehle purana
role tuple delete karta hai, phir naya likhta hai — **do alag calls**. Beech me
crash ho gaya toh user ke paas **koi role nahi** bachta, hamesha ke liye, aur
database me aisa kuch nahi jisse recover kiya ja sake.

Template runs teesra mechanism use karte hain — `workflow_outbox` — jiska apna
dispatcher hai, jo authorization waale ki lagbhag copy tha.

**Pattern ek baar copy-paste ho chuka hai.** Ye design uski jagah ek generic
queue laata hai jisme koi bhi package enqueue kar sake, aur ek controller jo
items ko registered handlers tak pahunchaye.

## Scope

Andar hai:

- Ek generic `work_queue` table aur `internal/queue` package.
- Authorization writes ko uspe le jaana — including wo assign/revoke paths jo
  abhi unprotected hain.
- Stack provisioning: `stacks` pe ek `status` column, aur `grant_stack_owner` +
  `mark_stack_ready` kinds jo use aage badhate hain (**revision 2**).
- `notify_user` kind — aage notifications ke liye reserve.
- Ek read endpoint jisse user apne queue kiye hue items dekh sake.

Bahar hai, jaan-boojh ke:

- `workflow_outbox` ko migrate karna. Wo aaj kaam kar raha hai. Pehle generic
  shape production me chal le; wo waise bhi Job-shaped hai, migration lagbhag
  sirf rename hai.
- Notification delivery banana. `internal/events` khaali stub hai — na email, na
  websocket, na webhook. Ye design sirf **jagah** banata hai jahan notification
  handler lagega. Bhejta kaun hai, uska apna design chahiye.
- Postgres aur OpenFGA ke beech drift detection.

## Decisions taken, and what was rejected

**Grants ka source of truth OpenFGA hi rahega.** Ek purane draft me `stack_grants`
table thi taaki desired state Postgres me rahe. Reject kar diya: uski dalil
last-owner invariant pe tiki thi, aur wo invariant safety-critical nikla hi
nahi. `can_manage_access` sirf `owner` se aata hai, toh zero-owner stack sirf
apni access badalne ki kshamta khoti hai — view, operate, approve sab chalte
rehte hain. Aur `isPlatformAdmin` OpenFGA check se pehle short-circuit ho jaata
hai, toh platform admin hamesha naya owner de sakta hai. Failure mode ek support
ticket hai, data loss nahi.

Toh queue **ek durable retry buffer hai jo guarantee deti hai ki accept kiye hue
intents eventually land honge** — system of record nahi.

Dobara sochna tab, jab grants ko **query** karna pade product data ki tarah —
"har wo stack jahan user X owner hai", user metadata ke saath join, access
reports. OpenFGA ka read API paginated hai aur join nahi karta. `stack_grants`
baad me jodna additive hai: OpenFGA se backfill karo aur read path flip kar do.
Wo queue ka rewrite nahi hai.

**Koi inline delivery attempt nahi.** API enqueue karke lautti hai; external
systems me sirf controller likhta hai. Har path async hai, aur **ek hi handler
code** chalta hai chahe trigger kisi ne bhi kiya ho. Keemat ye ki caller apna
change turant dekh nahi sakta; `status` column aur neeche wala sankra `GetStack`
exception us baat ko 404 ki jagah imaandar banate hain.

Revision 2 me inline fast path socha aur reject kiya gaya: wo provisioning window
hata deta, par sirf sync aur async paths ko alag karke — sync path ko follow-up
step khud chalana padta, enqueue karne ki jagah. Aur wahi duplication hai jisse
bachne ke liye "ek hi code path" waali dalil thi.

**Retry forever, capped backoff ke saath.** Koi dead-lettering nahi. Coalescing
isko safe banati hai: ek key pe ek hi row hoti hai, wo hamesha latest intent
rakhti hai, aur hamesha fail hone wali key sirf khud ko rokti hai.
`authorization_outbox` waala `failed_at` column hata diya — itne chhote payloads
me "unparseable payload" failure mode lagbhag hai hi nahi.

**Ek handler ek se zyada system chhoo sakta hai, bashart uska kaam idempotent
ho.** Dual-write problem *atomicity* ki hai; at-least-once delivery + idempotent
handler uski jagah le lete hain. Ye rule na hota toh har multi-system operation
ke liye kinds ki apni chain banani padti, aur har chain ka apna adhoora state
hota. Jahan ek handler me do domains awkward lagein, wahan **system ke hisaab
se** todo — jaise `grant_stack_owner` / `mark_stack_ready`.

## Superseded decisions

Revision 2 me ye badla. Upar ke sections update kar diye gaye hain; ye section
isliye hai taaki wajah na kho jaaye.

| Pehle | Ab | Kyun |
| --- | --- | --- |
| `CreateStack` `reconcile_stack_grant` enqueue karta tha | `grant_stack_owner` karta hai | Ek kind "stack bani" aur "role badla" dono nahi sambhal sakta; payload me discriminator hai hi nahi, aur `ModeReconcile` baad ka enqueue owner ko overwrite kar deta. |
| Owner grant `ModeReconcile` tha | `grant_stack_owner` `ModeJob` hai | Provisioning ek event hai, desired state nahi. `ModeJob` ka `do nothing` original payload bachata hai; `ModeReconcile` ka `do update` kisi bhi future repair/backfill path se owner badal deta. |
| `Handler.Deliver(ctx, item) error` | `Deliver(ctx, item) ([]Request, error)` | Chaining ab declared return value hai, side-effect nahi. Handler ko `Enqueuer` dependency nahi chahiye, aur chain ko unit test me assert kiya ja sakta hai. |
| `Handler` ek hi interface tha | `Spec` (kind, mode, key) `Handler` (spec + deliver) se alag | API sirf produce karti hai — usko `Key`/`Mode` chahiye, `Deliver` nahi. Wo sirf inhe paane ke liye `nil` dependencies ke saath handler bana rahi thi; doosre kind ke saath wo pattern tut jaata. |
| Stack readiness implicit thi | `stacks.status` | Iske bina API aisi stack pe 201 deti hai jo abhi usable nahi, aur kahin record hi nahi hota. |

Revision 2 me kya reject hua, aur kyun:

- **Ek hi handler jo OpenFGA aur Postgres dono kare.** Correct hai — retry usko
  safe bana deta hai — par ek unit me do domains aa jaate hain. Iski jagah do
  kinds me toda.
- **`CreateStack` me inline fast path.** Upar dekho.
- **Readiness ko queue se derive karna** (stack ready hai agar koi pending
  `grant_stack_owner` row nahi) `status` column ki jagah. Resource key se query
  chahiye hoti, aur prune ke baad "row nahi hai" aur "kabhi enqueue hi nahi hua"
  me farak hi nahi bachta — toh chhoot gaya enqueue "ready" jaisa dikhta.

## Architecture

```
API ──> InTx { domain write + queue.Enqueue } ──> COMMIT ──> 201
                                                    │
                                     ┌──────────────┘
                                     ▼
                              Controller: claim batch, route on `kind`
                                     │
                                     ▼
                          Handler.Deliver(item) ──> ([]Request, error)
                            ① external call                (outside any tx)
                            ② return follow-up requests
                                     │
                                     ▼
                          Controller: enqueue follow-ups,
                                      complete fenced on revision
```

Follow-up requests completing statement ke bahar enqueue hote hain. Ye isliye
safe hai kyunki har kind apni key deterministically banata hai aur `ModeJob`
duplicate key ko ignore karta hai — toh follow-up enqueue aur parent complete ke
beech crash hone pe dono bina nuksaan ke replay ho jaate hain.

Teen layers, aur boundary ka test ye hai ki kal Keycloak provisioning kind jodne
me ek nayi file bane aur `internal/queue` ya `internal/postgres` me **ek line
bhi na badle**:

- **`internal/queue`** — `Item`, `Kind`, `Mode`, `Spec`, `Registry`,
  `Controller`, neeche wale interfaces, aur tests ke liye in-memory backend.
  Grants, workflows, OpenFGA, Postgres — kisi ka pata nahi. Payload kabhi parse
  nahi karta.
- **`internal/postgres`** — `work_queue` ke upar `Enqueuer`, `Backend`, `Reader`
  implement karta hai. Kinds ka koi pata nahi.
- **Domain packages** — har kind ke liye ek `Spec` aur ek `Handler`.

### Stack creation, end to end

```
CreateStack, ek transaction:
    INSERT stacks       (status = 'provisioning')
    INSERT work_queue   (grant_stack_owner, ModeJob, key = stack:<id>)
  COMMIT  ──> 201 { id, status: "provisioning" }

t≈1s   grant_stack_owner  → OpenFGA owner tuple
                          → returns [mark_stack_ready]
t≈2s   mark_stack_ready   → UPDATE stacks SET status = 'ready'
```

Access t≈1s pe wapas aa jaata hai, jab tuple land hota hai. Label t≈2s pe pakadta
hai. 201 se t≈1s ke beech creator ke paas OpenFGA me koi grant hai hi nahi —
usi window ko neeche wala `GetStack` exception dhakta hai.

Ek ki jagah do kinds isliye, taaki har handler **ek hi system** chhue:
`grant_stack_owner` ke liye OpenFGA, `mark_stack_ready` ke liye Postgres. Dono
`internal/app` me rehte hain, kyunki stack provision karna app ka concern hai
aur `Service` ke paas dono dependencies pehle se hain.

## Schema

Do tables shaamil hain: neeche `work_queue`, aur `stacks` pe ek naya column
(`status`) jo Migration section me hai.

Migration `0012_work_queue.sql`:

```sql
create table work_queue (
    id            bigserial primary key,
    kind          text not null,
    resource_key  text not null,
    payload       jsonb not null,
    revision      bigint not null default 1,
    actor_subject text not null default '',
    tenant_id     text not null default '',
    available_at  timestamptz not null default now(),
    claimed_until timestamptz,
    attempts      integer not null default 0 check (attempts >= 0),
    last_error    text not null default '',
    created_at    timestamptz not null default now(),
    processed_at  timestamptz
);

-- Load-bearing: ek (kind, resource_key) pe zyada se zyada ek pending row.
-- Isse reconcile kinds ko coalescing milti hai AUR har kind ko per-key mutual
-- exclusion, kyunki doosra worker aisi row claim kar hi nahi sakta jo bann hi
-- nahi sakti.
create unique index work_queue_pending_key_idx
    on work_queue (kind, resource_key)
    where processed_at is null;

create index work_queue_ready_idx
    on work_queue (available_at, id)
    where processed_at is null;

create index work_queue_actor_idx
    on work_queue (tenant_id, actor_subject, created_at desc);
```

## Modes

Faisla karne wala sawaal: **agar paanch item jama ho jayein, toh unhe ek me
milana sahi hai — ya kaam kho jayega?**

|                     | `ModeReconcile`         | `ModeJob`              |
| ------------------- | ----------------------- | ---------------------- |
| payload kya hai     | desired state           | kaam khud              |
| key kya naam deti hai | resource               | event (unique)         |
| conflict pe         | overwrite, `revision++` | kuch nahi              |
| paanch jama hue     | ek delivery             | paanch deliveries      |
| milane se kya khota | kuch nahi               | kaam                   |
| replay safe         | apne aap                | handler ki zimmedari   |
| backlog kis se badhta | resources ki ginti    | events ki ginti        |

Default `ModeJob` rakho; wo kabhi kaam nahi khota. `ModeReconcile` tabhi jab
desired state ko ek value ki tarah naam de sako. "Is user ka is stack pe role X
hai" — haan. "Ye email bhejo" — nahi.

Shuruaati kinds:

```
reconcile_stack_grant   Reconcile   stack:A/user:X      role ek state hai
grant_stack_owner       Job         stack:A             creation ek event hai
mark_stack_ready        Job         stack:A             ek hi baar flip
notify_user             Job         notif:9c2/rev3      (handler abhi nahi)
start_template_run      Job         run:7f3a            (baad me migrate)
```

`grant_stack_owner` aur `mark_stack_ready` ek hi resource key share karte hain. Ye
theek hai — unique index `(kind, resource_key)` pe hai, toh takraav hota hi
nahi, aur har kind apne aap se mutually excluded rehta hai.

## The three queries

**Enqueue.** Conflict clause handler ke mode se chunta hai.

```sql
-- ModeReconcile: naya desired state jeetta hai, aur in-flight worker fence hota hai
insert into work_queue (kind, resource_key, payload, actor_subject, tenant_id)
values ($1, $2, $3, $4, $5)
on conflict (kind, resource_key) where processed_at is null
do update set payload  = excluded.payload,
              revision = work_queue.revision + 1;

-- ModeJob: wahi kaam dobara enqueue karna no-op hai
insert into work_queue (kind, resource_key, payload, actor_subject, tenant_id)
values ($1, $2, $3, $4, $5)
on conflict (kind, resource_key) where processed_at is null
do nothing;
```

Jo pending row backoff me hai, uspe naya intent coalesce hone pe `available_at`
nahi badalta. Isse outage ke doran backoff bacha rehta hai, par keemat ye ki naya
intent purani failure ke peechhe ruk jaata hai. Baad me tune kar sakte hain.

**Claim.** Batched, plain indexed scan, koi correlated subquery nahi. `kinds`
optional hai — isse aage chal ke kind ke hisaab se controller shard kar sakte
hain.

```sql
with candidate as (
    select id from work_queue
    where processed_at is null
      and available_at <= $1
      and (claimed_until is null or claimed_until <= $1)
      and ($4::text[] is null or kind = any($4))
    order by available_at, id
    for update skip locked
    limit $3
), claimed as (
    update work_queue q
       set claimed_until = $2, attempts = attempts + 1
      from candidate
     where q.id = candidate.id
 returning q.id, q.kind, q.resource_key, q.payload, q.revision,
           q.actor_subject, q.tenant_id, q.attempts
) select * from claimed;
```

**Complete.** Revision pe fenced.

```sql
update work_queue
   set processed_at = now(), claimed_until = null, last_error = ''
 where id = $1 and revision = $2 and processed_at is null;
```

Zero rows ka matlab hai ki item flight me tha aur naya intent aa gaya.
**Complete mat karo**: `claimed_until` clear karo, `available_at = now()` karo,
aur naye payload ke saath dobara chalne do. Is fence ke bina naya intent
chupchaap kho jaata hai.

## Resource keys

Key handler derive karta hai, column me store hoti hai, aur index use karta hai.
SQL me compute **nahi** hoti — warna payload ka structure queue ke schema me
ghus jaata aur genericity khatam ho jaati.

Grammar: `type:id` segments, `/` se juda, sabse important pehle. Inhe maujooda
canonical formatters se banao (`authz.Stack.String()`, `authz.Subject.String()`)
na ki `fmt.Sprintf` se — taaki key ka format us identity se kabhi alag na ho jaye
jise wo naam deti hai.

**Key derivation ek frozen contract hai.** Keys persist hoti hain, toh derivation
badalne se ek resource do formats me bat jaata hai, aur unique index concurrent
workers ko rokna band kar deta hai — wahi race wapas jise rokne ke liye ye design
bana. Badalna ho toh: ya producers band karke queue drain karo, ya naya kind naam
lao (`reconcile_stack_grant_v2`) aur purane ko purane format pe drain hone do. Ye
`Spec` ke doc comment me likha hona chahiye.

Mode aur key shape ka mel hona zaroori hai. Repeating key wala Job chupchaap kaam
nigal jaata hai; unique key wala Reconcile mutual exclusion band kar deta hai.
Kyunki ek hi `Spec` value me `Mode` aur `Key` dono hain, wo saath declare hote
hain aur caller ek ko doosre ke bina set kar hi nahi sakta.

## Package interfaces

```go
package queue

type Kind string

type Mode int

const (
    ModeReconcile Mode = iota // coalesce by key; payload is desired state
    ModeJob                   // distinct work; payload is immutable
)

// Request is what callers enqueue. The key is derived, not supplied.
type Request struct {
    Kind         Kind
    Payload      json.RawMessage
    ActorSubject string
    TenantID     string
}

// Item is a claimed row handed to a handler.
type Item struct {
    ID           int64
    Kind         Kind
    Key          string
    Payload      json.RawMessage
    Revision     int64
    ActorSubject string
    TenantID     string
    Attempts     int
}

// Spec declares a kind. It carries no dependencies, so a producer-only process
// can register kinds without constructing the handlers that deliver them.
type Spec struct {
    Kind Kind
    Mode Mode
    Key  func(payload json.RawMessage) (string, error)
}

// Handler is implemented by whichever package owns the target system.
//
// Deliver MUST be idempotent: the queue guarantees at-least-once delivery.
//
// The returned requests are follow-up work, enqueued by the controller after a
// successful delivery.
type Handler interface {
    Spec() Spec
    Deliver(ctx context.Context, item Item) ([]Request, error)
}

// Optional, discovered by type assertion.
type Describer interface {
    Describe(payload json.RawMessage) string
}

type Timings interface {
    MaxBackoff() time.Duration
}

type Enqueuer interface {
    Enqueue(ctx context.Context, requests ...Request) error
}

type Backend interface {
    Claim(ctx context.Context, now, leaseUntil time.Time, limit int, kinds []Kind) ([]Item, error)
    Complete(ctx context.Context, id, revision int64) (bool, error)
    Reschedule(ctx context.Context, id int64, availableAt time.Time, lastErr string) error
    Prune(ctx context.Context, before time.Time) (int64, error)
}

type Reader interface {
    ListByActor(ctx context.Context, tenantID, actorSubject string, limit int) ([]Status, error)
}
```

Do registries hain, kyunki dono binaries ko kind ke alag-alag hisse chahiye:

```go
// Producers ko sirf Kind, Mode, Key chahiye. Na dependency, na nil handler.
queue.NewSpecRegistry(app.GrantStackOwnerSpec, app.MarkStackReadySpec, authz.StackGrantSpec)

// Worker deliver bhi karta hai.
queue.NewRegistry(provisionHandler, readyHandler, grantHandler)
```

Dono construction pe duplicate kinds reject karte hain, taaki galat configure
kiya hua binary startup pe fail ho, delivery ke waqt nahi. `postgres.Store` ek
spec registry rakhta hai taaki `Enqueue` payload se key aur mode nikal sake;
callers `Request` dete hain aur key kabhi dekhte hi nahi.

`Controller` ek batch claim karta hai, bounded worker pool me baant deta hai
taaki ek slow handler baaki kinds ko bhookha na rakhe, aur har handler ka
returned follow-up complete karne se pehle enqueue karta hai.

**Jis item ka kind registered nahi hai, use reschedule karna hai — fail
kabhi nahi.** Rolling deploy me API aise kinds bana sakti hai jo worker ko abhi
pata nahi; unhe fail karna kaam hamesha ke liye kho dena hai. Retry-forever isko
apne aap theek kar deta hai.

Backoff: `1s * 2^(attempts-1)`, jittered, 5 minute ya handler ke `MaxBackoff()`
pe capped. Prune `processed_at < now() - 24h` waali rows hataata hai, controller
se har 10 minute.

## Transaction seam

Enqueue ko caller ke transaction me shaamil hona **hi** hai — yahi poori wajah
hai ki ye outbox hai, message broker nahi. Aaj koi unit-of-work helper nahi hai;
`Store` ke saare 28 methods apni-apni transaction kholte hain.

Ek jodo, bas utna hi jitna call sites ko chahiye. Naya dual-write point aane pe
hi badhega.

```go
type TxRepo interface {
    CreateStack(ctx context.Context, stack traits.Stack) error
    AppendAuditEvent(ctx context.Context, event traits.SecurityAuditEvent) error
}

type UnitOfWork interface {
    InTx(ctx context.Context, fn func(TxRepo, queue.Enqueuer) error) error
}
```

## Call sites

**`CreateStack`.** `stackOwnerIntentRepository` type assertion aur
`CreateStackWithOwnerIntent` dono khatam. Ek transaction stack ko
`status = 'provisioning'` ke saath insert karti hai aur `grant_stack_owner` request
bhi; response 201 usi status ke saath.

`grant_stack_owner` ka payload, key `stack:stack_abc`:

```json
{"stack_id": "stack_abc", "subject": "user:xyz"}
```

**`GetStack`.** Ek sankra exception jodta hai, kyunki 201 aur delivery ke beech
creator ke paas OpenFGA me koi grant nahi hota aur usko apni hi banai stack pe
404 milta:

```
PermissionView  AND  status = 'provisioning'  AND  stacks.created_by = caller
    → allow
baaki sab
    → OpenFGA, jaisa hai waisa
```

Sirf dekhne ke liye. Stack pe operate karna, template jodna, run chalana — ye sab
OpenFGA se hi hote rahenge, kyunki stack sach me ready nahi hai. `status` ke
`ready` hote hi exception lagna band ho jaata hai, toh steady state me ye doosra
authorization source nahi hai.

Ye exception **load-bearing hai, cosmetic nahi**: inline delivery na hone ki wajah
se sirf yahi provisioning window ko dhakta hai.

**`AssignStackRole`.** Asli fix. Aaj wo do unprotected OpenFGA calls karta hai.
Baad me: ek `InTx` jo audit event aur ek `reconcile_stack_grant` request likhta
hai jiska payload **desired** role hai. Purane role ka deletion poora khatam —
handler converge karta hai, toh delete-then-write ka window hai hi nahi.

**`RevokeStackRole`.** Wahi shape, payload me khaali role.

`reconcile_stack_grant` ka payload:

```json
{"stack_id": "stack_abc", "subject": "user:xyz", "role": "owner"}
{"stack_id": "stack_abc", "subject": "user:xyz", "role": ""}
```

Khaali role ka matlab "koi access nahi". Key: `stack:stack_abc/user:xyz`.

Last-owner guard OpenFGA se hi padhta rahega aur best-effort rahega, jaisa aaj
hai. Ab wo aisi state padh raha hai jo queue ki delivery latency jitni peechhe ho
sakti hai. Upar wale decision ke hisaab se accept.

## Handlers

### `reconcile_stack_grant` — `ModeReconcile`, key `stack:<id>/user:<sub>`

`internal/authz` me rehta hai, jisne purana `authdispatch` dispatcher loop
absorb kar liya aur sirf yahi handler rakha.

Converge: us stack pe subject ke maujooda tuples padho, delta nikalo, apply karo.
Iske liye OpenFGA adapter me ek chhoti cheez chahiye — `read` jo user pe bhi
filter kare, object pe bhi, kyunki `ListGrants` sirf object pe filter karta hai.

Apne aap idempotent: wahi desired state dobara lagana no-op hai. Koi follow-up
return nahi karta. `stacks.status` ko kabhi nahi chhuta, toh fail hone wala role
change kisi `ready` stack ko wapas regress nahi kar sakta.

### `grant_stack_owner` — `ModeJob`, key `stack:<id>`

`internal/app` me rehta hai. Sirf OpenFGA chhuta hai: owner tuple likhta hai,
phir ek `mark_stack_ready` request return karta hai.

`ModeReconcile` ki jagah `ModeJob` isliye ki provisioning ek event hai, aur
duplicate key pe `do nothing` record kiye hue owner ko kisi bhi baad ke enqueue
se overwrite hone se bachata hai.

Handler `Service.GrantStackOwner` ke upar ek patla adapter hai, taaki logic wahan
rahe jahan `Authorizer` pehle se hai.

### `mark_stack_ready` — `ModeJob`, key `stack:<id>`

`internal/app` me rehta hai. Sirf Postgres chhuta hai: `UPDATE stacks SET status
= 'ready'`. Banawat se hi idempotent. Koi follow-up nahi.

Kind aur resource key milke unique hain, toh `grant_stack_owner` ke saath
`stack:<id>` share karna collision nahi hai.

## Notifications

Notification alag table nahi, alag kind hai. `notify_user`, `ModeJob`, wahi
table, wahi controller. Kabhi notification volume grants ko bhookha rakhne lage,
toh usi table pe kind allowlist ke saath doosra controller chala do.

Notification ek follow-up request hai, jo `Deliver` se **external call safal hone
ke baad** return hoti hai. Pehle kabhi nahi — warna aise kaam ki khabar chali
jayegi jo baad me fail ho gaya.

Revision 1 me likha tha ki ye enqueue completing transaction me hona chahiye.
Zaroorat nahi hai, aur isliye `Backend.Complete` ka signature waisa hi rehta hai:
`ModeJob` duplicate key ignore karta hai, toh notification enqueue aur parent
complete ke beech crash hone pe parent replay hota hai, wahi key banti hai, aur
kuch naya insert nahi hota.

Ye poori safety **key ke deterministic hone** pe tiki hai. Notification key item
se banni chahiye — aur usme kuch aisa ho jo ek delivery ko doosri se alag kare,
jaise `notif:<stack>/<user>/rev<N>`. Random ya timestamp waali key har replay pe
duplicate message bhej degi.

`notify_user` ka handler is design ka hissa nahi hai — Scope dekho.

## Read path

`GET /v1/tenants/{tenant_id}/queue` wo items lautata hai jinka `actor_subject`
caller hai aur `tenant_id` match karta hai. Koi naya permission concept nahi:
tum sirf apne items dekh sakte ho.

Har row me: `kind`, `status`, `created_at`, `attempts`, `last_error`, aur handler
ke `Describe` se ek human-readable summary — taaki API layer kabhi kind-specific
JSON parse na kare. Raw payload nahi lautaya jaata.

Kyunki retry hamesha chalta hai, atka hua item hamesha pending dikhega. `attempts`
aur `last_error` hi "do second pehle queue hua" aur "ek ghante se fail ho raha
hai" me farak batate hain — dono dikhne chahiye.

## Migration

`0012_work_queue.sql` (ship ho chuka):

1. `work_queue` banao.
2. `authorization_outbox` ki unprocessed rows ko `reconcile_stack_grant` items ke
   tor pe backfill karo. `operation = 'grant'` us row ka role banega;
   `operation = 'revoke'` khaali role. `processed_at` ya `failed_at` set wali rows
   chhod do.

`0013_stack_status.sql` (revision 2):

```sql
alter table stacks add column status text not null default 'ready'
    check (status in ('provisioning', 'ready'));

-- Purani stacks ready hain: unke owner tuples purane synchronous path ne likhe
-- the. Exception sirf wo stack hai jiska grant abhi bhi queue me pada hai.
update stacks set status = 'provisioning'
where exists (
    select 1 from work_queue
     where processed_at is null
       and resource_key like 'stack:' || stacks.id || '%'
);
```

`default 'ready'` sirf maujooda rows ke liye hai; `CreateStack` explicitly
`'provisioning'` insert karta hai. Ek baar chalne wale migration me sequential
scan theek hai, toh `LIKE` ko koi index nahi chahiye.

Follow-up, jab koi row na bache: `authorization_outbox` table hataao.

`workflow_outbox` aur `internal/dispatch` ko haath nahi lagta.

## Testing

Repository ki maujooda table-driven style follow karo, tests pehle.

Unit, bina database, in-memory backend ke saath:

- Registry duplicate kinds reject kare; unknown kind reschedule ho, fail nahi.
- Backoff ka sequence aur cap, handler ke diye `MaxBackoff` ke saath bhi.
- Controller success pe complete kare, error pe reschedule kare, `last_error`
  record kare.
- Worker pool concurrency bound kare; slow handler doosre kinds ko na roke.
- Har spec ka `Key()` derivation, malformed payloads ke saath bhi.
- Controller handler ke returned follow-ups enqueue kare, aur handler ke error
  dene pe **na** kare.
- `grant_stack_owner` theek ek `mark_stack_ready` request return kare.
- `mark_stack_ready` do baar chalne pe status `ready` chhode aur dono baar error
  na de.
- `reconcile_stack_grant` koi follow-up na de aur status kabhi na chhue.

Integration, Postgres ke saath:

- Coalescing: ek key pe teen reconcile enqueue karne pe theek ek pending row
  bache, naye payload aur `revision = 3` ke saath.
- Mutual exclusion: do concurrent claim loops ek hi key do workers ko ek saath
  kabhi na dein.
- Revision fence: in-flight claim ke doran enqueue karne se `Complete` zero rows
  affect kare aur item naye payload ke saath dobara chale.
- `ModeJob` dedupe: wahi key dobara enqueue karna no-op ho aur revision na badhe.
- Rollback koi orphan queue row na chhode.
- Lease expiry ke baad chhodi hui row phir claimable ho.
- Prune retention se purani processed rows hataaye aur pending ko chhode.
- Migration backfill: pending queue row wali stack `provisioning` bane, baaki sab
  `ready`.

End-to-end:

- Role assign karo, controller chalne se pehle process maar do, restart karo,
  assert karo ki OpenFGA converge ho gaya.
- Non-admin `stack-creator` ban ke stack banao. 201 ke turant baad `GetStack`
  safal ho aur `provisioning` bataye. Controller chalne ke baad `ready` bataye,
  aur sirf OpenFGA path se bhi allow hota.

Doosra wala **non-admin** se hi chalana hoga. `isPlatformAdmin` OpenFGA check se
pehle short-circuit kar deta hai, toh admin us window me ghusta hi nahi aur test
exception ke bina bhi pass ho jayega.

## Scaling

Postgres bandhan nahi hai. Queue ka load administrative actions se aata hai, user
count se nahi — bade user base pe bhi role changes average ek item per second se
kaafi kam rehte hain, bas bulk onboarding me chhote bursts.

Pehle kya tutega, kram se: single-row polling (yahan batch claim se theek kiya),
phir OpenFGA ka write path, jo har item pe do round trips leta hai kyunki har
write ke baad `HIGHER_CONSISTENCY` confirm hota hai. Postgres usse kahin tez
khila sakta hai jitna wo pi sakta hai.

Dobara dekho jab **sustained pending depth 10k rows se upar** ho ya **p99
time-to-delivery 30 second se upar** — dono seedha `work_queue` se padhe ja sakte
hain. Agar table sach me chhoti pad jaye, toh use badalna nahi hai —
transactional-enqueue waali dalil scale pe aur mazboot hoti hai — balki use write
path rakh ke CDC se broker pe fan out karna hai. `Backend` seam isi liye hai.

## Known limitations

Ye accept ki hui hain, khuli nahi. Likhi isliye hain taaki koi inhe bug samajh ke
dobara na khode.

**Provisioning ke doran kiya gaya revoke undo ho sakta hai.** Agar koi
`grant_stack_owner` complete hone se pehle founding owner hata de, toh retry tuple
wapas likh dega. Window sankri hai — provisioning ke doran koi doosra owner hota
hi nahi jo revoke kar sake — par hai.

**Repair path ko current owner dena hoga.** Unique index sirf pending rows pe hai,
toh `grant_stack_owner` item complete aur prune hone ke baad usi stack pe naya
enqueue normally insert ho jayega. Kal koi repair/re-provision tool bane toh use
stack ka **maujooda** owner pass karna hoga, `stacks.created_by` nahi — warna wo
chupchaap original creator ko wapas bitha dega.

**Status access se lagbhag ek poll interval peechhe rehta hai.** Tuple t≈1s pe
aata hai, label t≈2s pe. Harmless, par jo UI `status` pe gate karega (API ke
apne jawab pe nahi) wo ek second slow lagega.

**`ListStackGrants` abhi bhi OpenFGA se padhta hai**, toh wo thodi der ke liye
caller ka apna kiya hua role change nahi dikhayega. Stack creation ke ulat yahan
koi `status` nahi hai jo isse dikha sake. Agar ye maayne rakhe: response me
pending queue rows merge karo (iske liye resource key se query chahiye, jo
jaan-boojh ke nahi banai), ya `stack_grants` pe dobara socho.

**Naya intent purani failure ka backoff virasat me leta hai.** Coalescing
`available_at` reset nahi karti. Jaan-boojh ke — warna bezaar user ka retry aise
system ko peetega jo pehle hi down hai — par outage ke turant baad role change
sust lag sakta hai.

**Stack creation ab chalte hue worker ke bina kaam nahi karegi.** Queue se pehle
API khud owner tuple likhti thi. Worker down ho toh nayi stacks hamesha
`provisioning` rahengi; `GetStack` exception unhe creator ko dikhata rahega, par
kisi aur ke liye wo usable nahi hongi.

## Open questions

1. **`notify_user` ka koi delivery mechanism nahi.** Kind aur mode yahan defined
   hain; jab tak notification infrastructure nahi banti, kuch bhejta hi nahi.
2. **`Describer` use hi nahi hota.** `ListByActor` `Summary` kabhi bharta nahi aur
   API kind ke naam pe fall back kar deti hai, toh `Describe` kabhi call hi nahi
   hota. Ya toh ise wire karo — matlab payload select karo aur reader ko registry
   do — ya interface hata do. Jise koi call na kare wo contract nahi hai.
3. **Audit events do alag tarikon se likhe jaate hain.** `CreateStack` transaction
   ke bahar likhta hai, best-effort; `AssignStackRole` aur `RevokeStackRole` andar
   likhte hain. Ek hi kism ka event, do guarantees. Ek chuno.
