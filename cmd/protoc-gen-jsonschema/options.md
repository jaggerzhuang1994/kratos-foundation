# Protocol Documentation
<a name="top"></a>

## Table of Contents

- [jsonschema.proto](#jsonschema-proto)
    - [ArrayKeywords](#pubg-jsonschema-ArrayKeywords)
    - [EnumOptions](#pubg-jsonschema-EnumOptions)
    - [EnumValueOptions](#pubg-jsonschema-EnumValueOptions)
    - [FieldOptions](#pubg-jsonschema-FieldOptions)
    - [FileOptions](#pubg-jsonschema-FileOptions)
    - [MessageOptions](#pubg-jsonschema-MessageOptions)
    - [NumericKeywords](#pubg-jsonschema-NumericKeywords)
    - [ObjectKeywords](#pubg-jsonschema-ObjectKeywords)
    - [PluginOptions](#pubg-jsonschema-PluginOptions)
    - [StringKeywords](#pubg-jsonschema-StringKeywords)
  
    - [Draft](#pubg-jsonschema-Draft)
    - [EnumOptions.MappingType](#pubg-jsonschema-EnumOptions-MappingType)
    - [PluginAdditionalProperties](#pubg-jsonschema-PluginAdditionalProperties)
  
    - [File-level Extensions](#jsonschema-proto-extensions)
    - [File-level Extensions](#jsonschema-proto-extensions)
    - [File-level Extensions](#jsonschema-proto-extensions)
    - [File-level Extensions](#jsonschema-proto-extensions)
    - [File-level Extensions](#jsonschema-proto-extensions)
  
- [Scalar Value Types](#scalar-value-types)



<a name="jsonschema-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## jsonschema.proto



<a name="pubg-jsonschema-ArrayKeywords"></a>

### ArrayKeywords



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| min_items | [uint32](#uint32) | optional |  |
| max_items | [uint32](#uint32) | optional |  |
| unique_items | [bool](#bool) | optional |  |






<a name="pubg-jsonschema-EnumOptions"></a>

### EnumOptions



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| mapping_type | [EnumOptions.MappingType](#pubg-jsonschema-EnumOptions-MappingType) |  |  |
| title | [string](#string) |  |  |
| description | [string](#string) |  |  |






<a name="pubg-jsonschema-EnumValueOptions"></a>

### EnumValueOptions



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| custom_value | [google.protobuf.Any](#google-protobuf-Any) |  |  |






<a name="pubg-jsonschema-FieldOptions"></a>

### FieldOptions



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| visibility_level | [uint32](#uint32) |  | WIP: visibility_level is used to determine which message should be generated. Currently not work. |
| title | [string](#string) |  |  |
| description | [string](#string) |  |  |
| nullable | [bool](#bool) |  |  |
| default | [google.protobuf.Any](#google-protobuf-Any) |  |  |
| array | [ArrayKeywords](#pubg-jsonschema-ArrayKeywords) |  |  |
| numeric | [NumericKeywords](#pubg-jsonschema-NumericKeywords) |  |  |
| string | [StringKeywords](#pubg-jsonschema-StringKeywords) |  |  |






<a name="pubg-jsonschema-FileOptions"></a>

### FileOptions



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| visibility_level | [uint32](#uint32) |  | WIP: visibility_level is used to determine which message should be generated. Currently not work. |
| entrypoint_message | [string](#string) |  | entrypoint_message is used which message should be entrypoint object of schema. default: inherit from PluginOptions.entrypoint_message |
| title | [string](#string) |  |  |
| description | [string](#string) |  |  |






<a name="pubg-jsonschema-MessageOptions"></a>

### MessageOptions



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| visibility_level | [uint32](#uint32) |  | WIP: visibility_level is used to determine which message should be generated. Currently not work. |
| title | [string](#string) |  |  |
| description | [string](#string) |  |  |
| object | [ObjectKeywords](#pubg-jsonschema-ObjectKeywords) |  |  |






<a name="pubg-jsonschema-NumericKeywords"></a>

### NumericKeywords



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| inclusive_minimum | [double](#double) |  |  |
| exclusive_minimum | [double](#double) |  |  |
| inclusive_maximum | [double](#double) |  |  |
| exclusive_maximum | [double](#double) |  |  |
| multiple_of | [int32](#int32) | optional |  |






<a name="pubg-jsonschema-ObjectKeywords"></a>

### ObjectKeywords



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| additional_properties | [bool](#bool) | optional |  |
| min_properties | [uint32](#uint32) | optional | repeated JsonSchema additional_properties = 10; |
| max_properties | [uint32](#uint32) | optional |  |






<a name="pubg-jsonschema-PluginOptions"></a>

### PluginOptions
Not extendable, just define structure
Plugin wide options


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| visibility_level | [uint32](#uint32) |  | WIP: visibility_level is used to determine which message should be generated. Currently not work. |
| entrypoint_message | [string](#string) |  | entrypoint_message 用来指定哪一个 Message 作为整个 JSON Schema 的入口（根对象）。 示例: --jsonschema_opt=entrypoint_message=MyMessage |
| output_file_suffix | [string](#string) |  | output_file_suffix 用来决定生成的输出文件名后缀。 取值必须以 .json、.yaml 或 .yml 结尾。 默认值: .schema.json 示例: --jsonschema_opt=output_file_suffix=.schema.json --jsonschema_opt=output_file_suffix=.schema.yaml |
| draft | [Draft](#pubg-jsonschema-Draft) |  | draft 用来指定生成 JSON Schema 时所使用的规范（Draft）版本。 取值必须是以下之一：Draft04、Draft05、Draft06、Draft07、Draft201909、Draft202012。 默认值: Draft202012 示例: --jsonschema_opt=draft=Draft202012 |
| mandatory_nullable | [bool](#bool) |  | mandatory_nullable 用来决定插件是否将 optional 字段视为 nullable。 许多编程语言并不区分 undefined 和 null。 但像 JavaScript、TypeScript 这样的脚本语言可以明确区分这两者。 默认情况下，optional 字段同时被视为可以为 null，也可以不定义（undefined）。 默认值: false 示例: --jsonschema_opt=mandatory_nullable=true --jsonschema_opt=mandatory_nullable=false |
| preserve_proto_field_names | [bool](#bool) |  | preserve_proto_field_names 用来决定输出的 JSON 字段名是否与 proto 中的字段名完全一致。 如果不启用该选项，字段名将优先使用字段上声明的 json_name，否则会自动转换为 lowerCamelCase。 这种默认行为与 Protobuf 官方的标准 JSON 格式（ProtoJSON）的处理方式保持一致。 默认值: false 示例: --jsonschema_opt=preserve_proto_field_names=true --jsonschema_opt=preserve_proto_field_names=false |
| additional_properties | [PluginAdditionalProperties](#pubg-jsonschema-PluginAdditionalProperties) |  | additional_properties 选项用于控制所有 message 的 additionalProperties 属性。 如果你希望为所有 message 统一设置 additionalProperties，可以使用 always_true 或 always_false。 如果你只想为未显式定义该属性的 message 设置 additionalProperties，可以使用 default_true 或 default_false。 默认值: DoNothing 示例: --jsonschema_opt=additional_properties=AlwaysTrue --jsonschema_opt=additional_properties=AlwaysFalse --jsonschema_opt=additional_properties=DefaultTrue --jsonschema_opt=additional_properties=DefaultFalse --jsonschema_opt=additional_properties=DoNothing |
| respect_protojson_presence | [bool](#bool) |  | 该选项用于决定插件是否遵循 ProtoJSON 格式中字段的 presence 语义。 presence 语义的字段：optional字段、message类型字段、oneof里的字段 如果设置为 true，对于那些具有 presence 语义的字段，插件会在生成的 JSON Schema 中为这些字段添加 required 关键字。 默认值: false 示例: --jsonschema_opt=respect_protojson_presence=true --jsonschema_opt=respect_protojson_presence=false |
| respect_protojson_int64 | [bool](#bool) |  | 该选项用于决定插件是否遵循 ProtoJSON 格式中对 int64 字段的处理方式。 如果设置为 true，int64 字段在生成的 JSON Schema 中会被当作字符串（string）处理；否则会被当作数值（number）处理。 默认值: false 示例: --jsonschema_opt=respect_protojson_int64=true --jsonschema_opt=respect_protojson_int64=false |
| merge | [string](#string) |  | 合并其他 jsonschema 需要 draft 一致才可以合并 默认值：空 示例: --jsonschema_opt=respect_protojson_int64=true |






<a name="pubg-jsonschema-StringKeywords"></a>

### StringKeywords



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| min_length | [uint32](#uint32) | optional |  |
| max_length | [uint32](#uint32) | optional |  |
| pattern | [string](#string) |  |  |
| format | [string](#string) |  |  |
| enum | [string](#string) | repeated |  |





 


<a name="pubg-jsonschema-Draft"></a>

### Draft


| Name | Number | Description |
| ---- | ------ | ----------- |
| DraftDefault | 0 |  |
| Draft04 | 1 |  |
| Draft05 | 2 |  |
| Draft06 | 3 |  |
| Draft07 | 4 |  |
| Draft201909 | 5 |  |
| Draft202012 | 6 |  |



<a name="pubg-jsonschema-EnumOptions-MappingType"></a>

### EnumOptions.MappingType


| Name | Number | Description |
| ---- | ------ | ----------- |
| MapToString | 0 |  |
| MapToNumber | 1 |  |
| MapToCustom | 2 |  |



<a name="pubg-jsonschema-PluginAdditionalProperties"></a>

### PluginAdditionalProperties


| Name | Number | Description |
| ---- | ------ | ----------- |
| DefaultAdditionalProperties | 0 |  |
| AlwaysTrue | 1 | always_true: all messages to have additional_properties set to true |
| AlwaysFalse | 2 | always_false: all messages to have additional_properties set to false |
| DefaultTrue | 3 | default_true: all messages will have additional_properties set to true if not defined |
| DefaultFalse | 4 | default_false: all messages will have additional_properties set to false if not defined |
| DoNothing | 5 | do_nothing: do not set additional_properties default value |


 


<a name="jsonschema-proto-extensions"></a>

### File-level Extensions
| Extension | Type | Base | Number | Description |
| --------- | ---- | ---- | ------ | ----------- |
| enum | EnumOptions | .google.protobuf.EnumOptions | 11344 |  |
| enum_value | EnumValueOptions | .google.protobuf.EnumValueOptions | 11345 |  |
| field | FieldOptions | .google.protobuf.FieldOptions | 11343 |  |
| file | FileOptions | .google.protobuf.FileOptions | 11341 |  |
| message | MessageOptions | .google.protobuf.MessageOptions | 11342 |  |

 

 



## Scalar Value Types

| .proto Type | Notes | C++ | Java | Python | Go | C# | PHP | Ruby |
| ----------- | ----- | --- | ---- | ------ | -- | -- | --- | ---- |
| <a name="double" /> double |  | double | double | float | float64 | double | float | Float |
| <a name="float" /> float |  | float | float | float | float32 | float | float | Float |
| <a name="int32" /> int32 | Uses variable-length encoding. Inefficient for encoding negative numbers – if your field is likely to have negative values, use sint32 instead. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="int64" /> int64 | Uses variable-length encoding. Inefficient for encoding negative numbers – if your field is likely to have negative values, use sint64 instead. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="uint32" /> uint32 | Uses variable-length encoding. | uint32 | int | int/long | uint32 | uint | integer | Bignum or Fixnum (as required) |
| <a name="uint64" /> uint64 | Uses variable-length encoding. | uint64 | long | int/long | uint64 | ulong | integer/string | Bignum or Fixnum (as required) |
| <a name="sint32" /> sint32 | Uses variable-length encoding. Signed int value. These more efficiently encode negative numbers than regular int32s. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="sint64" /> sint64 | Uses variable-length encoding. Signed int value. These more efficiently encode negative numbers than regular int64s. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="fixed32" /> fixed32 | Always four bytes. More efficient than uint32 if values are often greater than 2^28. | uint32 | int | int | uint32 | uint | integer | Bignum or Fixnum (as required) |
| <a name="fixed64" /> fixed64 | Always eight bytes. More efficient than uint64 if values are often greater than 2^56. | uint64 | long | int/long | uint64 | ulong | integer/string | Bignum |
| <a name="sfixed32" /> sfixed32 | Always four bytes. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="sfixed64" /> sfixed64 | Always eight bytes. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="bool" /> bool |  | bool | boolean | boolean | bool | bool | boolean | TrueClass/FalseClass |
| <a name="string" /> string | A string must always contain UTF-8 encoded or 7-bit ASCII text. | string | String | str/unicode | string | string | string | String (UTF-8) |
| <a name="bytes" /> bytes | May contain any arbitrary sequence of bytes. | string | ByteString | str | []byte | ByteString | string | String (ASCII-8BIT) |

