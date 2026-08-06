#the subprocess to analyze pdfs
import sys
import os

def test():
    from marker.converters.pdf import PdfConverter
    from marker.models import create_model_dict
    import pymupdf4llm
    
    print("Initializing models...")
    converter = PdfConverter(artifact_dict=create_model_dict())

    files = sys.argv[1:]
    
    if not files:
        print("Please provide at least one PDF file as an argument.")
        return

    for f in files:
        print(f"\nProcessing: {f}")
        
        base_name = os.path.splitext(os.path.basename(f))[0]
        
        try:
            print("  -> Converting with Marker...")
            md1 = converter(f).markdown
            path1 = f"{base_name}_marker.md"
            
            with open(path1, "w", encoding="utf-8") as file1:
                file1.write(md1)
            print(f"     Saved Marker output to: {path1}")
            
            print("  -> Converting with PyMuPDF4LLM...")
            md2 = pymupdf4llm.to_markdown(f)
            path2 = f"{base_name}_pymupdf.md"
            
            with open(path2, "w", encoding="utf-8") as file2:
                file2.write(md2)
            print(f"     Saved PyMuPDF output to: {path2}")
            
        except Exception as e:
            print(f"  -> Error processing {f}: {e}")

def main():
    if len(sys.argv) < 2:
        print("usage: read_pdf.py <pdf_path>", file=sys.stderr)
        sys.exit(1)

    pdf_path = sys.argv[1]

    if not os.path.isfile(pdf_path):
        print(f"pdf not found: {pdf_path}", file=sys.stderr)
        sys.exit(1)

    try:
        from marker.converters.pdf import PdfConverter
        from marker.models import create_model_dict
        converter = PdfConverter(artifact_dict=create_model_dict())
        markdown = converter(pdf_path).markdown
    except ImportError:
        markdown = None
    except Exception as e:
        print(f"marker failed, falling back to pymupdf4llm: {e}", file=sys.stderr)
        markdown = None

    if markdown is None:
        try:
            import pymupdf4llm
            markdown = pymupdf4llm.to_markdown(pdf_path)
        except ImportError:
            print(
                "no pdf backend installed - run: pip install pymupdf4llm (or marker-pdf for better accuracy)",
                file=sys.stderr,
            )
            sys.exit(1)
        except Exception as e:
            print(f"pdf conversion failed: {e}", file=sys.stderr)
            sys.exit(1)

    print(markdown)

if __name__ == "__main__":
    # if you wanna test comment main and uncomment test
    #test()
    main()
