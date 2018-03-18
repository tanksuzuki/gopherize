# Gopherize

Detect faces in the photo and make it Gopher.

__before__

![](doc/tanksuzuki_before.png)

__after__

![](doc/tanksuzuki_after.png)

For details, please visit [https://gopherize.com](https://gopherize.com).

## How to use

1. Run `dep ensure`
1. Enable Cloud Vision API on [Google Cloud Console](https://console.cloud.google.com)
1. Create API key on credentials page
1. Create `/gopherize/app/credentials.json` file and put your API key
1. Run `goapp serve app/` on repository root
1. Access `http://localhost:8080`
1. Enjoy :tada:

__`/gopherize/app/credentials.json`__

```
{
  "key": "put_your_api_key_here"
}
```

## Contribution

1. Fork ([https://github.com/tanksuzuki/gopherize/fork](https://github.com/tanksuzuki/gopherize/fork))
1. Create a feature branch
1. Commit your changes
1. Rebase your local changes against the master branch
1. Create new Pull Request

## Author

[Asuka Suzuki](https://twitter.com/tanksuzuki)