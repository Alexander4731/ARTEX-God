"""Enable Windows UTF-8 paths in the bundled PostgreSQL executable manifests."""
import ctypes
from ctypes import wintypes
from pathlib import Path
import sys
import xml.etree.ElementTree as ET

kernel = ctypes.WinDLL('kernel32', use_last_error=True)

def api(name, result, *arguments):
    function = getattr(kernel, name)
    function.restype = result
    function.argtypes = arguments
    return function

load_library = api('LoadLibraryExW', ctypes.c_void_p, wintypes.LPCWSTR, ctypes.c_void_p, wintypes.DWORD)
free_library = api('FreeLibrary', wintypes.BOOL, ctypes.c_void_p)
find_resource = api('FindResourceExW', ctypes.c_void_p, ctypes.c_void_p, ctypes.c_void_p, ctypes.c_void_p, wintypes.WORD)
load_resource = api('LoadResource', ctypes.c_void_p, ctypes.c_void_p, ctypes.c_void_p)
lock_resource = api('LockResource', ctypes.c_void_p, ctypes.c_void_p)
resource_size = api('SizeofResource', wintypes.DWORD, ctypes.c_void_p, ctypes.c_void_p)
begin_update = api('BeginUpdateResourceW', ctypes.c_void_p, wintypes.LPCWSTR, wintypes.BOOL)
update_resource = api('UpdateResourceW', wintypes.BOOL, ctypes.c_void_p, ctypes.c_void_p, ctypes.c_void_p, wintypes.WORD, ctypes.c_void_p, wintypes.DWORD)
end_update = api('EndUpdateResourceW', wintypes.BOOL, ctypes.c_void_p, wintypes.BOOL)
callback_type = ctypes.WINFUNCTYPE(wintypes.BOOL, ctypes.c_void_p, ctypes.c_void_p, ctypes.c_void_p, wintypes.WORD, ctypes.c_ssize_t)
enum_languages = api('EnumResourceLanguagesW', wintypes.BOOL, ctypes.c_void_p, ctypes.c_void_p, ctypes.c_void_p, callback_type, ctypes.c_ssize_t)

ASM1 = 'urn:schemas-microsoft-com:asm.v1'
ASM3 = 'urn:schemas-microsoft-com:asm.v3'
WS2019 = 'http://schemas.microsoft.com/SMI/2019/WindowsSettings'
ET.register_namespace('', ASM1)
ET.register_namespace('asmv3', ASM3)
ET.register_namespace('ws2019', WS2019)

def enable(path):
    module = load_library(str(path), None, 0x22)
    if not module:
        raise ctypes.WinError(ctypes.get_last_error())
    manifests = {}
    try:
        languages = []
        callback = callback_type(lambda module, kind, name, language, data: languages.append(language) or True)
        if not enum_languages(module, 24, 1, callback, 0):
            error = ctypes.get_last_error()
            if error not in (1813, 1814, 1815):
                raise ctypes.WinError(error)
            languages = [0]
        for language in languages:
            resource = find_resource(module, 24, 1, language)
            if resource:
                pointer = lock_resource(load_resource(module, resource))
                if not pointer:
                    raise ctypes.WinError(ctypes.get_last_error())
                document = ET.fromstring(ctypes.string_at(pointer, resource_size(module, resource)))
            else:
                document = ET.Element('{' + ASM1 + '}assembly', manifestVersion='1.0')
            application = document.find('{' + ASM3 + '}application')
            if application is None:
                application = ET.SubElement(document, '{' + ASM3 + '}application')
            settings = application.find('{' + ASM3 + '}windowsSettings')
            if settings is None:
                settings = ET.SubElement(application, '{' + ASM3 + '}windowsSettings')
            code_page = settings.find('{' + WS2019 + '}activeCodePage')
            if code_page is None:
                code_page = ET.SubElement(settings, '{' + WS2019 + '}activeCodePage')
            code_page.text = 'UTF-8'
            manifests[language] = ET.tostring(document, encoding='utf-8', xml_declaration=True)
    finally:
        free_library(module)
    handle = begin_update(str(path), False)
    if not handle:
        raise ctypes.WinError(ctypes.get_last_error())
    committed = False
    try:
        for language, data in manifests.items():
            buffer = ctypes.create_string_buffer(data)
            if not update_resource(handle, 24, 1, language, buffer, len(data)):
                raise ctypes.WinError(ctypes.get_last_error())
        if not end_update(handle, False):
            raise ctypes.WinError(ctypes.get_last_error())
        committed = True
    finally:
        if not committed:
            end_update(handle, True)
    print('Enabled UTF-8 paths:', path.name)

if __name__ == '__main__':
    directory = Path(sys.argv[1]).resolve()
    for name in ('initdb.exe', 'postgres.exe', 'pg_ctl.exe', 'psql.exe'):
        enable(directory / name)
