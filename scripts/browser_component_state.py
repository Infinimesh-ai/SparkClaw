"""Read a private snapshot of Chromium's extension-specific LevelDB; never write it."""
import ctypes as C
import ctypes.util
import json
from pathlib import Path
import shutil
import tempfile


def read_extension_state(directory):
    library = ctypes.util.find_library('leveldb')
    if not library:
        raise ValueError('libleveldb is required for managed browser readiness')
    db = C.CDLL(library)
    signatures = {
        'leveldb_options_create': ([], C.c_void_p),
        'leveldb_options_destroy': ([C.c_void_p], None),
        'leveldb_open': ([C.c_void_p, C.c_char_p, C.POINTER(C.c_char_p)], C.c_void_p),
        'leveldb_close': ([C.c_void_p], None),
        'leveldb_readoptions_create': ([], C.c_void_p),
        'leveldb_readoptions_destroy': ([C.c_void_p], None),
        'leveldb_create_iterator': ([C.c_void_p, C.c_void_p], C.c_void_p),
        'leveldb_iter_destroy': ([C.c_void_p], None),
        'leveldb_iter_seek_to_first': ([C.c_void_p], None),
        'leveldb_iter_valid': ([C.c_void_p], C.c_ubyte),
        'leveldb_iter_next': ([C.c_void_p], None),
        'leveldb_iter_key': ([C.c_void_p, C.POINTER(C.c_size_t)], C.c_void_p),
        'leveldb_iter_value': ([C.c_void_p, C.POINTER(C.c_size_t)], C.c_void_p),
        'leveldb_iter_get_error': ([C.c_void_p, C.POINTER(C.c_char_p)], None),
        'leveldb_free': ([C.c_void_p], None),
    }
    for name, (args, result) in signatures.items():
        function = getattr(db, name)
        function.argtypes, function.restype = args, result
    with tempfile.TemporaryDirectory(prefix='sparkclaw-extension-state-') as tmp:
        snapshot = Path(tmp) / 'db'
        shutil.copytree(directory, snapshot)
        options = db.leveldb_options_create()
        error = C.c_char_p()
        handle = db.leveldb_open(options, str(snapshot).encode(), C.byref(error))
        db.leveldb_options_destroy(options)
        if error.value:
            db.leveldb_free(error)
            raise ValueError('extension snapshot unavailable; retry readiness')
        read_options = db.leveldb_readoptions_create()
        iterator = db.leveldb_create_iterator(handle, read_options)
        values = {}
        try:
            db.leveldb_iter_seek_to_first(iterator)
            while db.leveldb_iter_valid(iterator):
                size = C.c_size_t()
                key = db.leveldb_iter_key(iterator, C.byref(size))
                key = C.string_at(key, size.value).decode()
                value = db.leveldb_iter_value(iterator, C.byref(size))
                value = json.loads(C.string_at(value, size.value))
                values[key] = value.get('value', value) if isinstance(value, dict) else value
                db.leveldb_iter_next(iterator)
            db.leveldb_iter_get_error(iterator, C.byref(error))
            if error.value:
                db.leveldb_free(error)
                raise ValueError('extension snapshot iteration failed')
        finally:
            db.leveldb_iter_destroy(iterator)
            db.leveldb_readoptions_destroy(read_options)
            db.leveldb_close(handle)
        return values
