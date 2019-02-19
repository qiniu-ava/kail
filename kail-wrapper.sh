#!/bin/bash

set -o errexit -o pipefail -o nounset

! getopt --test > /dev/null
if [[ ${PIPESTATUS[0]} -ne 4 ]]; then
    exit 1
fi

OPTIONS=haso:
LONGOPTS=help,append,stdout,out:

! PARSED=$(getopt --options=$OPTIONS --longoptions=$LONGOPTS --name "$0" -- "$@")
if [[ ${PIPESTATUS[0]} -ne 0 ]]; then
    exit 2
fi
eval set -- "$PARSED"

help() {
    echo "$0 [-a|--append] [-s|--stdout] [-o|--out outFile] -- [kail options]"
}

if [ "$#" -eq 1 ]; then
    help
    exit 0
fi

bin=kail
append=
stdout=
out=
while true; do
    case "$1" in
        -h|--help)
            help
            exit 0
            ;;
        -a|--append)
            append=y
            shift
            ;;
        -s|--stdout)
            stdout=y
            shift
            ;;
        -o|--output)
            out="$2"
            shift 2
            ;;
        --)
            shift
            break
            ;;
        *)
            exit 3
            ;;
    esac
done

if [ -n "$out" ]; then
    # create out dir if not exist
    mkdir -p $(dirname "$out")

    if [ -n "$append" ]; then
        if [ -n "$stdout" ]; then
            $bin "$@" |tee -a "$out"
        else
            $bin "$@" >> "$out"
        fi
    elif [ -f "$out" ]; then
        # backup original file
        cp "$out" "$out.$(date +%s)"

        if [ -n "$stdout" ]; then
            $bin "$@" |tee "$out"
        else
            $bin "$@" > "$out"
        fi
    fi
elif [ -n "$stdout" ]; then
    $bin "$@"
else
    echo "Either stdout or out file should be specified."
    exit 0
fi

