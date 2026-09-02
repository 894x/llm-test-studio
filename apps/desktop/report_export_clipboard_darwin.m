#import <AppKit/AppKit.h>

int llm_test_studio_copy_png_to_clipboard(const void *bytes, long length) {
    @autoreleasepool {
        if (bytes == NULL || length <= 0) {
            return 0;
        }

        NSData *png = [NSData dataWithBytes:bytes length:(NSUInteger)length];
        NSBitmapImageRep *image = [NSBitmapImageRep imageRepWithData:png];
        if (image == nil) {
            return 0;
        }

        NSData *tiff = [image TIFFRepresentation];
        NSMutableArray<NSPasteboardType> *types = [NSMutableArray arrayWithObject:NSPasteboardTypePNG];
        if (tiff != nil) {
            [types addObject:NSPasteboardTypeTIFF];
        }

        NSPasteboard *pasteboard = [NSPasteboard generalPasteboard];
        [pasteboard declareTypes:types owner:nil];
        BOOL pngWritten = [pasteboard setData:png forType:NSPasteboardTypePNG];
        if (tiff != nil) {
            [pasteboard setData:tiff forType:NSPasteboardTypeTIFF];
        }
        return pngWritten ? 1 : 0;
    }
}
