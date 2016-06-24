jsondir
-------
<https://godoc.org/go.spiff.io/jsondir>

   $ go get go.spiff.io/jsondir

jsondir is a tool to map directory structures and their contents to JSON.

It walks a directory tree and convert files found to what it thinks is an
appropriate JSON representation. Boolean values are true/TRUE and false/FALSE,
numerics are any normal value handled by strconv.ParseInt, floats any string
convertible by strconv.ParseFloat, the string "null" or "NULL" is a null value,
and everything else is treated as a string. Type precedence is null, boolean,
integer, float, and then string as a catch-all. Empty files are empty strings,
and jsondir will by default trim trailing spaces.

Files ending in an '@' (at sign) are treated as raw JSON values and will be
unmarshaled upon loading to verify they're valid. Invalid data is a failure.
Each tree walked is emitted as a separate JSON blob, with each blob separated by
a newline. If the output is not compact, there is still a newline separating the
start and end of the JSON blobs.

If the -x flag is set, executable files will be run to generate JSON output.
This can be used to nest jsondir calls if necessary (e.g., including a separate
directory tree). By default, executable files are run in a temporary directory,
just for the sake of confusing them. You can disable this by passing -rx or -nt
to either execute files in the directory they're located in or in jsondir's
working directory, respectively.

By default, dot files are ignored. If you pass an ignore parameter, this default
no longer applies.

Each path is converted to a JSON value. The path may refer to a file or
directory -- in the case of a directory, it will produce either an object or an
array. If a directory's name ends in "[]" (minus quotes), the directory is
treated as an array. If the name ends in "{}", it's an object. The first of
either "{}" or "[]" are trimmed from key names when nesting directories. As
such, to include "[]" at the end of a directory's key name without turning the
directory into an array, you can name it "yourDirectory[]{}".


Usage
-----

   jsondir [OPTIONS] [path...]


Options
-------

All boolean flags can be passed as their name only to imply true. E.g., -v is
the same as -v=true. All boolean flags default to false except for -c (compact).

-v=true|false
   Enable verbose logging. Useful for debugging, not much else.

-c=true|false
   Emit compact output. This defaults to false if stdout is a TTY.

-s=true|false
   Whether to follow symlinks. By default, symlinks are ignored.

-ws=true|false
   Whether to keep trailing whitespace.

-x=true|false
   Run executables to produce output. Off by default for obvious sanity reasons.

-nt=true|false
   If -x is true, -nt tells jsondir to run executables from the PWD instead of
   a temporary directory. If your executables are safe and well-behaved, this is
   probably fine.

-rx=true|false
   If -x is true, -rx tells jsondir to run executables from the executables'
   directory instead of the PWD. It implies -nt.

-i PATTERN
   Ignore the given file pattern. Follows Go's filepath.Match rules. If the
   pattern does not contain slashes, it will only be matched against a file's
   basename.


License
-------
jsondir is licensed under the two-clause BSD license. You can read the license
in LICENSE.txt.
