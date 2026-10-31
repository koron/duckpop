LIBDIR=./_lib/duckdb-shared-libs-windows-amd64

if [ ! -d "$LIBDIR" ]; then
  echo "Skipped: no libraries found"
  reutrn 0 2>/dev/null || exit 0
fi

absdir=$(realpath "$LIBDIR")

export CGO_LDFLAGS="-lduckdb -L$absdir"
export GOFLAGS="-tags=duckdb_use_lib"
export PATH="$absdir:$PATH"

unset add_path
unset absdir
