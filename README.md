# koron/duckpop

[![PkgGoDev](https://pkg.go.dev/badge/github.com/koron/duckpop)](https://pkg.go.dev/github.com/koron/duckpop)
[![Actions/Go](https://github.com/koron/duckpop/workflows/Go/badge.svg)](https://github.com/koron/duckpop/actions?query=workflow%3AGo)
[![Go Report Card](https://goreportcard.com/badge/github.com/koron/duckpop)](https://goreportcard.com/report/github.com/koron/duckpop)
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/koron/duckpop)

![Duckpop Logo 512x512](./image/Duckpop-512x512.png)

Duckpop は短寿命の DuckDB のインメモリインスタンスを提供する HTTP サーバーです。

cURL などにより DuckDB プレイグラウンドへクエリーを投げることができます。
インスタンスの寿命およびスコープはTCP接続の間に限定されます。
つまり Keep Alive 接続や HTTP/2 セッションの間だけ専用インスタンスが維持されます。

## Features

その他に以下のような機能があります

-   対応している出力フォーマット: CSV, HTML, Markdown, table (in plain text), AVRO
-   同時に起動できるDuckDBインスタンス数≒接続数 (デフォルト: 20)
-   DuckDBインスタンス毎の…
    -   スレッド数 (デフォルト: 1)
    -   メモリ (デフォルト: 1GiB)
    -   テンポラリディレクトリのサイズ (デフォルト: 10GiB)
    -   初期設定のロック/アンロック (デフォルト: ロック)
    -   カスタマイズ可能な初期化スクリプト: [S3認証情報などの設定](#s3認証情報などの設定) に利用可能
-   アクセスログ
    表示内容の詳細は [Accesslog format](#accesslog-format) を参照
-   維持されているDuckDBインスタンスの一覧
-   実行中のクエリーの一覧
-   個別のクエリーのキャンセル
-   認証機能: クエリーとそのキャンセルに認証を必須とする
    - アクセスログ等に認証のIDを記録
    - BASIC認証/トークン認証
    - 認可不要の設定: 認証しなくても実行できる。認証情報があればIDは記録する
-   1クエリーの最大サイズを1MiBに制限
    - `-maxbodysize {size}` オプションで変更可能
-   その他のオプション
    -   `-debug` - デバッグログの有効化 (開発用)
    -   `-pprof` - pprofエンドポイントの有効化 (開発用)
    -   `-pidfile {filename}` - プロセスIDの記録。ログローテーションのために SIGHUPを送る対象
    -   `-accesslog.file` - アクセスログの出力先。デフォルトは標準出力
    -   `-accesslog.format` - `text` もしくは `json`
    -   `-ui.resourcedir` - `/ui/` でホストされる埋め込み簡易UIを、指定したディレクトリの内容で置き換える (開発用)

## Motivation

Duckpop は以下の動機で生まれました。

-   DuckDBはOLAPツールとして S3 のファイルを読めて便利
-   ローカルのDuckDBから S3 にアクセスすると転送料がかかる → EC2で実行すれば転送料がかからない上に速くて「お得」
    -   集計結果のたった数行が欲しい
    -   巨大なファイルを舐めるから、総量は大きくなる
-   EC2インスタンスにログインしてDuckDBを実行するのは意外と手間 → HTTPサーバーでラップして cURL でアクセスするのが楽
    -   組織内で共有
    -   必要なのはかなり低めのセキュリティ

## Getting Started

### Install and Update

最新バイナリを以下からダウンロードして、実行ファイル `duckpop` を適切なディレクトリに配置してください。

<https://github.com/koron/duckpop/releases/latest>

`duckpop` を起動すると、カレントディレクトリに `.duckpop` というディレクトリを作成し、
その中に DuckDB が必要とするディレクトリならびにファイルが作られます。
それらにはダウンロードした DuckDB Extensions やテンポラリファイルが含まれます。
詳しい仕様は [DB設定のデフォルト値](#db設定のデフォルト値) も参照。

自分でGoを用いてビルドする場合は、以下のようにコマンドを実行します。

```console
$ go install github.com/koron/duckpop@latest
```

duckdb/duckdb-go-bindings を利用している関係上、
以下のアーキテクチャ以外ではこのコマンドが機能しません。

-   Windows (AMD64)
-   Linux (AMD64)
-   Linux (ARM64)
-   macOS (AMD64)
-   macOS (ARM64)

### First touch

まず duckpop を起動します。
指定可能な起動オプションを知りたい場合は `duckpop -h` を実行してください。

```console
$ duckpop
2026/03/18 16:24:28 INFO listening on addr=localhost:9281
```

次に別の端末から cURL を用いてクエリーを duckpop へ投げます。
デフォルトの出力フォーマットはCSVです。

```console
$ curl 'http://127.0.0.1:9281/' -d "SELECT version() as VER"
VER
v1.5.2
```

同じクエリーの結果を別のフォーマット(table)で受信します。

```console
$ curl 'http://127.0.0.1:9281/?f=table' -d "SELECT version() as VER"
┌────────┐
│  VER   │
├────────┤
│ v1.5.3 │
└────────┘
```

## Endpoints

### クエリー実行

-   Path: `/`
-   Method: `POST` or `GET`
-   Request Parameters:
    -   クエリーの内容: BODY, `q` クエリー文字列, `query` クエリー文字列 (前の物が優先)

        クエリーは `;` で接続することで1度に複数のクエリーを順番に実行できます。
        その場合、出力は最後のクエリーのものになります。

    -   出力フォーマット指定: `format` クエリー文字列, `f` クエリー文字列 (前の物が優先)

        現在指定可能なフォーマットは次の5つ: `csv` (default), `html`, `markdown`, `table`, `avro`

        各フォーマットにパラメータを指定できる場合は、以下の形式で指定します。

        ```
        {format}
        {format},{param1}:{value1}
        {format},{param1}:{value1},{param2}:{value2},...,{paramN}:{valueN}
        ```

-   Response Parameters:
    -   Status Code: `200`
    -   ヘッダー:
        -   `Content-Type`: 出力フォーマット次第
        -   `Duckpop-Authnid` - 認証ID
        -   `Duckpop-Connectionid` - 接続ID (DuckDBインスタンスの識別子)
        -   `Duckpop-Queryid` - クエリーID
        -   `Duckpop-Duration` - クエリーにかかった時間
    -   ボディ: クエリーの結果

リクエストに `Expect: 100-continue` ヘッダーを追加すると、
Duckpopはクエリーを実際に実行する直前で `100 Continue` を返すようになる。
その際、一緒に `Duckpop-Connectionid` と `Duckpop-Queryid` ヘッダーが返される。
これは、特に後者のクエリーIDをクエリーキャンセルに使えるようにするための動作である。

クエリーを未指定で GET すると `/ui/` (簡易UI)へリダイレクトする。

### 死活監視

-   Path: `/ping/`
-   Method: `GET`
-   Request Parameters: なし
-   Response Parameters:
    -   Status Code: `200`
    -   ボディ: `OK\r\n`

### サーバー設定情報

-   Path: `/config/`
-   Method: `GET`
-   Request Parameters: なし
-   Response Parameters:
    -   Status Code: `200`
    -   ヘッダー:
        -   `Content-Type`: `application/json`
    -   ボディ: Duckpopサーバーの起動時設定のJSON

        例:

        ```json
        {
          "EnableDebugLog": false,
          "Address": "localhost:9281",
          "MaxDB": 20,
          "PIDFile": "",
          "AccessLogFile": "",
          "AccessLogFormat": "text",
          "AuthnFile": "",
          "NoAuthz": false,
          "DBHomeDir": "/var/run/duckpop",
          "DBThreads": 1,
          "DBMemoryLimit": "1GiB",
          "DBMaxTempDirSize": "10GiB",
          "DBExternalAccess": true,
          "DBLockConfig": true,
          "DBInitQuery": "",
          "UIResourceFS": {}
        }
        ```


### DuckDBインスタンス(接続)一覧

-   Path: `/status/connections/`
-   Method: `GET`
-   Request Parameters: なし
-   Response Parameters:
    -   Status Code: `200`
    -   ヘッダー:
        -   `Content-Type`: `application/jsonlines`
    -   ボディ: 1行 = 1つのDuckDBインスタンスを示すJSONオブジェクト

        JSONオブジェクトの例:

        ```json
        {
          "ID": "{接続ID}",
          "DBStats": {
            "MaxOpenConnections": 0,
            "OpenConnections": 0,
            "InUse": 0,
            "Idle": 0,
            "WaitCount": 0,
            "WaitDuration": 0,
            "MaxIdleClosed": 2,
            "MaxIdleTimeClosed": 0,
            "MaxLifetimeClosed": 0
          }
        }
        ```

        参照: JSONの基になっているGoの型 <https://pkg.go.dev/database/sql#DBStats>

### クエリー一覧

-   Path: `/status/queries/`
-   Method: `GET`
-   Request Parameters: なし
-   Response Parameters:
    -   Status Code: `200`
    -   ヘッダー:
        -   `Content-Type`: `application/jsonlines`
    -   ボディ: 1行 = 1つのDuckDBインスタンスを示すJSONオブジェクト

        JSONオブジェクトのスキーマ解説:

        ```json
        {
          "ID":       "{クエリーID}"
          "ConnID":   "{接続ID}",
          "Query":    "{クエリー}",
          "Start":    "{開始時刻}",
          "Duration": "{経過時間}"
        }
        ```
        
### クエリーキャンセル

-   Path: `/status/queries/{クエリーID}`
-   Method: `DELETE`
-   Request Parameters: なし
-   Response Parameters:
    -   Status Code: `204`
    -   ヘッダー:
        -   `Duckpop-Authnid` - 認証ID
    -   ボディ: なし

キャンセルされたクエリーのリクエストには `504 Gateway Timeout` が返される。

### その他のパス

-   `/ui/` - 簡易UI
    -   `/ui/editor/` - クエリーエディタ
    -   `/ui/connections/` - DB接続一覧
    -   `/ui/queries/` - クエリー一覧 (キャンセル操作可)
-   `/shared/` - 共有ディレクトリの内容

## 認証・認可機能

起動時に `-authnfile {auth.json}` オプションで、認証情報を記録したJSONファイルを引数に指定すると、認証・認可機能が利用できます。
認証・認可機能はクエリー実行とクエリーキャンセルを、 `Authorization` ヘッダーで認証・認可によって、保護します。
`Authorization` ヘッダーに、JSONに記録された認証情報にマッチする `Basic` もしくは `Bearer` トークンを渡すことで、認証及び認可が成功します。
この時、認証・認可に成功したIDがアクセスログの `authn_id` に記録されます。
また同じIDがレスポンスの `Duckpop-Authnid` にも記載されます。

起動時に `-noauthz` 引数を指定すると、認証機能は有効化されますが認可機能は無効化されます。
つまり `Authorization` ヘッダーが無い場合や、内容が認証情報にマッチしない場合でも、クエリーの実行やキャンセルが可能です。
その際には ID はアクセスログにも `Duckpop-Authnid` にも記録されません。

参照: [認証情報のJSONスキーマ](#認証情報のjsonスキーマ)

## ディレクトリ

Duckpop では共有ディレクトリとプライベートディレクトリを提供しています。

### 共有ディレクトリ

共有ディレクトリの内容はすべてのTCP接続間で共有されます。
そのためクエリーの結果を保存し、再利用するのに向いています。
共有ディレクトリのパスは関数 `public_dir(name)` で取得できます。
以下は DuckDB の設定情報を共有ディレクトリのファイル settings.csv へ出力するコマンドの例です。

```console
$ curl http://127.0.0.1:9281/ -d "COPY (SELECT * FROM duckdb_settings()) TO (public_dir('settings.csv'))"
Count
150
```

共有ディレクトリの同名のファイルは常に上書きされるので、
間違えて上書きして古い内容を消してしまわないためには、
適切な命名ルールが必要になります。

#### ブラウジング

共有ディレクトリはHTTPブラウザやクライアントから
`/shared/` にアクセスすることで内容を確認できます。
ブラウザで表示されるディレクトリ一覧には
各ファイルの削除ボタンが付いており、クリックで直ちに削除できます。
`/shared/` は単なるHTTPファイルサーバーだけではなく
[WebDAV][webdav] を実装しています。
それにより WebDAV クライアントや cURL を用いても
ディレクトリの作成、新規ファイルのアップロード、
ファイル及びディレクトリの削除が可能です。

WebDAVによる操作の具体的な方法は以下のコマンド例を参照してください:

```sh
# ディレクトリの作成 (MKCOL メソッド)
curl -X MKCOL http://127.0.0.1:9281/shared/newdir

# 新規ファイルのアップロード
curl -X PUT http://127.0.0.1:9281/shared/hello.txt -d "Hello World"

# ディレクトリ・ファイルの削除
curl -X DELETE http://127.0.0.1:9281/shared/delete_target
```

WebDAV 固有の変更を伴う操作には認証・認可が適用されます。

[webdav]:https://ja.wikipedia.org/wiki/WebDAV


### プライベートディレクトリ

プライベートディレクトリは TCP 接続毎にユニークなディレクトリを提供するので、
1接続で複数のクエリーを投げる際に、中間結果をファイルへ出力し再利用する等の目的で利用できます。
なおプライベートディレクトリは TCP 接続が失われる際に、その内容と一緒に自動的に破棄・削除されます。
以下は DuckDB の設定情報をプライベートディレクトリのファイル settings.csv へ出力するコマンドの例です。

```console
$ curl http://127.0.0.1:9281/ -d "COPY (SELECT * FROM duckdb_settings()) TO (private_dir('settings.csv'))"
Count
150
```

### それ以外のディレクトリ

Duckpop はデフォルトでは `enable_external_access` を `true` に設定しています。

また共有ディレクトリとプライベートディレクトリを `allowed_directories` に設定しています。
しかし `enable_external_access=true` であるため
共有ディレクトリとプライベートディレクトリの外へもアクセスできるようになっています。

アクセスを厳密に共有ディレクトリとプライベートディレクトリだけに制限するには、 `enable_external_access` を `false` に設定してください。
起動オプションは `-db.externalaccess=false` です。
ただしこのオプションの主作用として HTTP や S3 で外部へアクセスはできなくなります。

## Appendix

### Accesslog format

アクセスログの項目名と内容は以下の通り

|     Name      |        Description        | Nullable |
|---------------|---------------------------|---------:|
| `time`        | Timestamp                 | false    |
| `level`       | Log level (fixed: `INFO`) | false    |
| `msg`         | Message (fixed: `access`) | false    |
| `remote_addr` | Remote address            | false    |
| `authn_id`    | Authenticated ID          | true     |
| `method`      | Requested method          | false    |
| `path`        | Requested path            | false    |
| `proto`       | Requested HTTP version    | false    |
| `referer`     | Referer                   | true     |
| `user_agent`  | User Agent                | true     |
| `status`      | Status code               | false    |
| `size`        | Response size             | false    |
| `conn_id`     | Connection ID             | true     |
| `query`       | Query                     | true     |
| `duration`    | Take time for query       | true     |

起動引数 `-accesslog.format` で、アクセスログのフォーマットを指定できる。
有効な値は `text` と `json` でデフォルトは `text` 。
`json` 指定時は JSONL (もしくは NDJSON) 形式で、そのまま DuckDB により読み込み可能。

`text` ログのサンプル

    time=2026-03-19T17:30:26.696+09:00 level=INFO msg=access remote_addr=127.0.0.1:32919 method=GET path=/ping/ proto=HTTP/1.1 user_agent=curl/8.19.0 status=200 size=4 conn_id=C_a544d397
    time=2026-03-19T17:30:26.750+09:00 level=INFO msg=access remote_addr=127.0.0.1:32920 method=POST path="/?f=table" proto=HTTP/1.1 user_agent=curl/8.19.0 status=200 size=123 conn_id=C_6d827f53 query="SELECT version() as VER" duration=1.1263ms

`json` ログのサンプル

    {"time":"2026-03-19T17:31:38.1425577+09:00","level":"INFO","msg":"access","remote_addr":"127.0.0.1:35570","method":"GET","path":"/ping/","proto":"HTTP/1.1","user_agent":"curl/8.19.0","status":200,"size":4,"conn_id":"C_b447774a"}
    {"time":"2026-03-19T17:31:38.1979854+09:00","level":"INFO","msg":"access","remote_addr":"127.0.0.1:35571","method":"POST","path":"/?f=table","proto":"HTTP/1.1","user_agent":"curl/8.19.0","status":200,"size":123,"conn_id":"C_89993ede","query":"SELECT version() as VER","duration":0}

### DB設定のデフォルト値

|           Name            |             Description                    |
|---------------------------|--------------------------------------------|
| `home_directory`          | `./.duckpop`。引数`-db.homedir`で設定可  |
| `extension_directory`     | `home_directory` + `/extensions`           |
| `secret_directory`        | `home_directory` + `/stored_secrets`       |
| `allowed_directories`     | 共有ディレクトリ、プライベートディレクトリ |
| `temp_directory`          | `home_directory` + `/tmp`                  |
| `max_temp_directory_size` | `10GiB`。引数`-db.maxtempdirsize`で設定可  |
| `lock_configuration`      | `true`。引数`-db.lockconfig=false`で解除可 |

-   共有ディレクトリ: `home_directory` + `/shared`
-   プライベートディレクトリ: `home_directory` + `/private`

DuckDBの設定一覧: <https://duckdb.org/docs/stable/configuration/overview>

### 認証情報のJSONスキーマ

-   ルート要素は認証情報オブジェクトの配列
-   認証情報オブジェクトの中身
    -   `id` - 認証情報のID。ログなどに記録される。ファイル内でユニークでなければならない。
    -   `type` - `"basic"` (BASIC認証) もしくは `"bearer"` (APIトークン)の何れか
    -   `user` - `type` が `"basic"` の時に必須なオブジェクト
        -   `name` - ユーザー名
        -   `password` - パスワード

        認証には次の形式の `Authorization` ヘッダーが必要

            `Authorization: Basic {base64({name}:{password})}`

    -   `token` - `type` が `"bearer"` の時に必須なトークン文字列

        認証には次の形式の `Authorization` ヘッダーが必要

            `Authorization: Bearer {token}`

    -   `init_query` - 初期化クエリーの文字列。
        特定の認証を利用した際に、スレッド数やメモリ割り当ての上限を引き上げる目的で利用できます。

<details>
<summary>設定ファイルのサンプル</summary>

```json
[
  {
    "id": "token1",
    "type": "bearer",
    "token": "token-0123456789abcdef"
  },
  {
    "id": "token2",
    "type": "bearer",
    "token": "foobarbaz"
  },
  {
    "id": "user1",
    "type": "basic",
    "user": {
      "name": "user1",
      "password": "abcd1234"
    }
  },
  {
    "id": "user2",
    "type": "basic",
    "user": {
      "name": "user2",
      "password": "xyz789"
    }
  },
  {
    "id": "threads-2",
    "type": "bearer",
    "token": "token-threads-2",
    "init_query": "SET threads = 2"
  }
]
```

</details>

<details>
<summary>cURL での使い方の例</summary>

```console
$ curl -v -u 'user2:xyz789' 'http://127.0.0.1:9281/' -d "SELECT version() as VER"
*   Trying 127.0.0.1:9281...
* Established connection to 127.0.0.1 (127.0.0.1 port 9281) from 127.0.0.1 port 48884
* using HTTP/1.x
* Server auth using Basic with user 'bar'
> POST / HTTP/1.1
> Host: 127.0.0.1:9281
> Authorization: Basic YmFyOnh5ejc4OQ==
> User-Agent: curl/8.19.0
> Accept: */*
> Content-Length: 23
> Content-Type: application/x-www-form-urlencoded
>
* upload completely sent off: 23 bytes
< HTTP/1.1 200 OK
< Content-Type: text/csv
< Duckpop-Authnid: bar
< Duckpop-Connectionid: C_c650b7ea
< Duckpop-Duration: 2.1053ms
< Date: Wed, 18 Mar 2026 04:11:09 GMT
< Content-Length: 11
<
VER
v1.5.2
* Connection #0 to host 127.0.0.1:9281 left intact

$ curl -v -H 'Authorization: Bearer foobarbaz' 'http://127.0.0.1:9281/' -d "SELECT version() as VER"
*   Trying 127.0.0.1:9281...
* Established connection to 127.0.0.1 (127.0.0.1 port 9281) from 127.0.0.1 port 36235
* using HTTP/1.x
> POST / HTTP/1.1
> Host: 127.0.0.1:9281
> User-Agent: curl/8.19.0
> Accept: */*
> Authorization: Bearer foobarbaz
> Content-Length: 23
> Content-Type: application/x-www-form-urlencoded
>
* upload completely sent off: 23 bytes
< HTTP/1.1 200 OK
< Content-Type: text/csv
< Duckpop-Authnid: token2
< Duckpop-Connectionid: C_a2551a00
< Duckpop-Duration: 0s
< Date: Wed, 18 Mar 2026 04:13:04 GMT
< Content-Length: 11
<
VER
v1.5.2
* Connection #0 to host 127.0.0.1:9281 left intact
```

</details>

<details>
<summary>サーバーの起動方法とアクセスログの例</summary>

```console
$ ./duckpop -authnfile ./testdata/authn.json
2026/03/18 13:15:40 INFO listening on addr=localhost:9281
127.0.0.1:57166 "user2" [18/Mar/2026:13:15:48 +0900] "POST / HTTP/1.1" "-" "curl/8.19.0" 200 11 C_7ca8851b "SELECT version() as VER" 545µs
127.0.0.1:57167 "token2" [18/Mar/2026:13:15:52 +0900] "POST / HTTP/1.1" "-" "curl/8.19.0" 200 11 C_6533f2af "SELECT version() as VER" 588.2µs
```
</details>

### S3認証情報などの設定

S3認証情報などの秘密情報(=シークレット)の設定は `CREATE SECRET` 文にて行います。
以下は、そのTCP接続でのみ有効なS3アクセスのためのシークレットを登録する例です。

```sql
CREATE SECRET my_secret (
    TYPE s3,
    KEY_ID 'my_secret_key',
    SECRET 'my_secret_value',
    REGION 'my_region'
);
```

シークレット管理の完全な解説は <https://duckdb.org/docs/stable/configuration/secrets_manager> を参照してください。

特に `CREATE PERSISTENT SECRET (...);` でシークレットを登録すると、
`secret_directory` 以下のファイルに永続化され、
以後すべての TCP 接続において共有されます。

利用可能なシークレットは `FROM duckdb_secrets()` で確認できます。

### Windows でのビルド

MSYS2のUCRT64のgccが15.2から16.1に変更された関係上、
duckdb/duckdb-go-bindings に付属のコンパイル済みスタティックライブラリが
gcc 16.1 では正常にリンクできない。

そのため自前で duckdb/duckdb をビルドした上で、以下のようなコンパイルオプション
が必要になる。

```console
$ CGO_ENABLED=1 \
  CPPFLAGS="-DDUCKDB_STATIC_BUILD" \
  CGO_LDFLAGS="-lduckdb_bundle -lws2_32 -lwsock32 -lrstrtmgr -lstdc++ -lm --static -L../duckdb/build/release" \
  go build -tags=duckdb_use_static_lib
```

テストも同様で、実行方法は以下のようになる。

```
$ CGO_ENABLED=1 \
  CPPFLAGS="-DDUCKDB_STATIC_BUILD" \
  CGO_LDFLAGS="-lduckdb_bundle -lws2_32 -lwsock32 -lrstrtmgr -lstdc++ -lm --static -L../duckdb/build/release" \
  go test -tags=duckdb_use_static_lib ./...
```

#### DuckDB のスタティックライブラリのビルド方法

```sh
cmake -B build/release \
  -DCMAKE_BUILD_TYPE=Release \
  -DBUILD_UNITTESTS=OFF \
  -DBUILD_SHELL=OFF \
  -DBUILD_SHARED_LIBS=OFF \
  -DEXTENSION_STATIC_BUILD=1 \
  -DBUILD_EXTENSIONS="icu;json;parquet;autocomplete;core_functions;tpcds;tpch" \
  -G "Ninja"

cmake --build build/release --config Release

make bundle-library-obj
```
