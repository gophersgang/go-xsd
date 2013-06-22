package xsd

import (
	"bytes"
	"encoding/xml"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-utils/ufs"
	"github.com/go-utils/unet"
	"github.com/go-utils/ustr"
)

const (
	goPkgPrefix     = ""
	goPkgSuffix     = "_go"
	protSep         = "://"
	xsdNamespaceUri = "http://www.w3.org/2001/XMLSchema"
)

var (
	loadedSchemas = map[string]*Schema{}
)

type Schema struct {
	elemBase
	XMLName            xml.Name          `xml:"schema"`
	XMLNamespacePrefix string            `xml:"-"`
	XMLNamespaces      map[string]string `xml:"-"`
	XMLIncludedSchemas []*Schema         `xml:"-"`
	XSDNamespacePrefix string            `xml:"-"`
	XSDParentSchema    *Schema           `xml:"-"`

	hasAttrAttributeFormDefault
	hasAttrBlockDefault
	hasAttrElementFormDefault
	hasAttrFinalDefault
	hasAttrLang
	hasAttrId
	hasAttrSchemaLocation
	hasAttrTargetNamespace
	hasAttrVersion
	hasElemAnnotation
	hasElemsAttribute
	hasElemsAttributeGroup
	hasElemsComplexType
	hasElemsElement
	hasElemsGroup
	hasElemsInclude
	hasElemsImport
	hasElemsNotation
	hasElemsRedefine
	hasElemsSimpleType

	loadLocalPath, loadUri string
}

func (me *Schema) allSchemas() (schemas []*Schema) {
	schemas = append(schemas, me)
	for _, ss := range me.XMLIncludedSchemas {
		schemas = append(schemas, ss.allSchemas()...)
	}
	return
}

func (me *Schema) collectGlobals(bag *PkgBag) {
	for _, att := range me.Attributes {
		bag.allAtts = append(bag.allAtts, att)
	}
	for _, agr := range me.AttributeGroups {
		bag.allAttGroups = append(bag.allAttGroups, agr)
	}
	for _, el := range me.Elements {
		bag.allElems = append(bag.allElems, el)
	}
	for _, egr := range me.Groups {
		bag.allElemGroups = append(bag.allElemGroups, egr)
	}
	for _, not := range me.Notations {
		bag.allNotations = append(bag.allNotations, not)
	}
	for _, ss := range me.XMLIncludedSchemas {
		ss.collectGlobals(bag)
	}
}

func (me *Schema) globalComplexType(bag *PkgBag, name string) (ct *ComplexType) {
	var imp string
	for _, ct = range me.ComplexTypes {
		if bag.resolveQnameRef(ustr.PrefixWithSep(me.XMLNamespacePrefix, ":", ct.Name.String()), "T", &imp) == name {
			return
		}
	}
	for _, ss := range me.XMLIncludedSchemas {
		if ct = ss.globalComplexType(bag, name); ct != nil {
			return
		}
	}
	ct = nil
	return
}

func (me *Schema) globalElement(bag *PkgBag, name string) (el *Element) {
	var imp string
	if len(name) > 0 {
		var rname = bag.resolveQnameRef(name, "", &imp)
		for _, el = range me.Elements {
			if bag.resolveQnameRef(ustr.PrefixWithSep(me.XMLNamespacePrefix, ":", el.Name.String()), "", &imp) == rname {
				return
			}
		}
		for _, ss := range me.XMLIncludedSchemas {
			if el = ss.globalElement(bag, name); el != nil {
				return
			}
		}
	}
	el = nil
	return
}

func (me *Schema) globalSubstitutionElems(el *Element) (els []*Element) {
	var elName = el.Ref.String()
	if len(elName) == 0 {
		elName = el.Name.String()
	}
	for _, tle := range me.Elements {
		if (tle != el) && (len(tle.SubstitutionGroup) > 0) {
			if tle.SubstitutionGroup.String()[(strings.Index(tle.SubstitutionGroup.String(), ":")+1):] == elName {
				els = append(els, tle)
			}
		}
	}
	for _, inc := range me.XMLIncludedSchemas {
		els = append(els, inc.globalSubstitutionElems(el)...)
	}
	return
}

func (me *Schema) MakeGoPkgSrcFile() (goOutFilePath string, err error) {
	var goOutDirPath = filepath.Join(filepath.Dir(me.loadLocalPath), goPkgPrefix+filepath.Base(me.loadLocalPath)+goPkgSuffix)
	goOutFilePath = filepath.Join(goOutDirPath, path.Base(me.loadUri)+".go")
	var bag = newPkgBag(me)
	for _, inc := range me.XMLIncludedSchemas {
		bag.Schema = inc
		inc.makePkg(bag)
	}
	bag.Schema = me
	me.hasElemAnnotation.makePkg(bag)
	bag.appendFmt(true, "")
	me.makePkg(bag)
	if err = ufs.EnsureDirExists(filepath.Dir(goOutFilePath)); err == nil {
		err = ufs.WriteTextFile(goOutFilePath, bag.assembleSource())
	}
	return
}

func (me *Schema) onLoad(rootAtts []xml.Attr, loadUri, localPath string) (err error) {
	var tmpUrl string
	var sd *Schema
	loadedSchemas[loadUri] = me
	me.loadLocalPath, me.loadUri = localPath, loadUri
	me.XMLNamespaces = map[string]string{}
	for _, att := range rootAtts {
		if att.Name.Space == "xmlns" {
			me.XMLNamespaces[att.Name.Local] = att.Value
		} else if len(att.Name.Space) > 0 {

		} else if att.Name.Local == "xmlns" {
			me.XMLNamespaces[""] = att.Value
		}
	}
	for k, v := range me.XMLNamespaces {
		if v == xsdNamespaceUri {
			me.XSDNamespacePrefix = k
		} else if v == me.TargetNamespace.String() {
			me.XMLNamespacePrefix = k
		}
	}
	if len(me.XMLNamespaces["xml"]) == 0 {
		me.XMLNamespaces["xml"] = "http://www.w3.org/XML/1998/namespace"
	}
	me.XMLIncludedSchemas = []*Schema{}
	for _, inc := range me.Includes {
		if tmpUrl = inc.SchemaLocation.String(); strings.Index(tmpUrl, protSep) < 0 {
			tmpUrl = path.Join(path.Dir(loadUri), tmpUrl)
		}
		if sd = loadedSchemas[tmpUrl]; sd == nil {
			if sd, err = LoadSchema(tmpUrl, len(localPath) > 0); err != nil {
				return
			}
		}
		sd.XSDParentSchema = me
		me.XMLIncludedSchemas = append(me.XMLIncludedSchemas, sd)
	}
	me.initElement(nil)
	return
}

func (me *Schema) RootSchema() *Schema {
	if me.XSDParentSchema != nil {
		return me.XSDParentSchema.RootSchema()
	}
	return me
}

func ClearLoadedSchemasCache() {
	loadedSchemas = map[string]*Schema{}
}

func loadSchema(r io.Reader, loadUri, localPath string) (sd *Schema, err error) {
	var data []byte
	var rootAtts []xml.Attr
	if data, err = ioutil.ReadAll(r); err == nil {
		var t xml.Token
		sd = new(Schema)
		for xd := xml.NewDecoder(bytes.NewReader(data)); err == nil; {
			if t, err = xd.Token(); err == nil {
				if startEl, ok := t.(xml.StartElement); ok {
					rootAtts = startEl.Attr
					break
				}
			}
		}
		if err = xml.Unmarshal(data, sd); err == nil {
			err = sd.onLoad(rootAtts, loadUri, localPath)
		}
		if err != nil {
			sd = nil
		}
	}
	return
}

func loadSchemaFile(filename string, loadUri string) (sd *Schema, err error) {
	var file *os.File
	if file, err = os.Open(filename); err == nil {
		defer file.Close()
		sd, err = loadSchema(file, loadUri, filename)
	}
	return
}

func LoadSchema(uri string, localCopy bool) (sd *Schema, err error) {
	var protocol, localPath string
	var rc io.ReadCloser

	if pos := strings.Index(uri, protSep); pos < 0 {
		protocol = "http" + protSep
	} else {
		protocol = uri[:pos+len(protSep)]
		uri = uri[pos+len(protSep):]
	}
	if localCopy {
		if localPath = filepath.Join(PkgGen.BaseCodePath, uri); !ufs.FileExists(localPath) {
			if err = ufs.EnsureDirExists(filepath.Dir(localPath)); err == nil {
				err = unet.DownloadFile(protocol+uri, localPath)
			}
		}
		if err == nil {
			if sd, err = loadSchemaFile(localPath, uri); sd != nil {
				sd.loadLocalPath = localPath
			}
		}
	} else if rc, err = unet.OpenRemoteFile(protocol + uri); err == nil {
		defer rc.Close()
		sd, err = loadSchema(rc, uri, "")
	}
	return
}
